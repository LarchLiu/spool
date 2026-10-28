package workspace

import (
	"container/list"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

const (
	historyCacheFiles = 64
	historyCacheBytes = 64 * 1024 * 1024
)

var errHistoryChanged = errors.New("native session changed while reading history")

// Only provider-owned data is shared. Chat IDs, permissions and live state are
// applied after the cached parts have been copied into a response.
type nativeHistoryCache struct {
	mu       sync.Mutex
	files    map[string]*nativeHistory
	fileLRU  list.List
	bytes    int
	maxFiles int
	maxBytes int
	index    func(chatmodel.Backend, string, int64, int64) (agent.NativeSessionIndex, error)
	readTurn func(chatmodel.Backend, string, agent.NativeTurn) (chatmodel.Transcript, error)
	titles   agent.CodexSessionTitles
}

type nativeHistory struct {
	mu      sync.Mutex
	key     string
	backend chatmodel.Backend
	path    string
	info    os.FileInfo
	indexed bool
	index   []agent.NativeTurn
	title   string
	parts   []chatmodel.Transcript
	bytes   int
	lru     *list.Element
}

func sameHistoryFile(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

func (c *nativeHistoryCache) file(backend chatmodel.Backend, path string) (*nativeHistory, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	key := string(backend) + ":" + path
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(path)
	if previous := c.files[key]; previous != nil {
		if err == nil && sameHistoryFile(previous.info, info) {
			c.fileLRU.MoveToFront(previous.lru)
			return previous, nil
		}
		c.removeFile(previous)
	}
	if err != nil {
		return nil, err
	}
	if c.files == nil {
		c.files = make(map[string]*nativeHistory)
	}
	file := &nativeHistory{key: key, backend: backend, path: path, info: info}
	file.lru = c.fileLRU.PushFront(file)
	c.files[key] = file
	limit := c.maxFiles
	if limit <= 0 {
		limit = historyCacheFiles
	}
	for len(c.files) > limit {
		c.removeFile(c.fileLRU.Back().Value.(*nativeHistory))
	}
	return file, nil
}

// Serialize readers of the same file, not unrelated conversations. The first
// reader populates the index/parts; simultaneous viewers then reuse that work.
func (c *nativeHistoryCache) read(backend chatmodel.Backend, path string, use func(*nativeHistory) error) error {
	for range 3 {
		file, err := c.file(backend, path)
		if err != nil {
			return err
		}
		file.mu.Lock()
		if !file.indexed {
			index := c.index
			if index == nil {
				index = agent.IndexSessionTo
			}
			var snapshot agent.NativeSessionIndex
			snapshot, err = index(backend, file.path, 0, file.info.Size())
			file.index, file.title = snapshot.Turns, snapshot.Title
			file.indexed = err == nil
		}
		if err == nil {
			err = use(file)
		}
		info, statErr := os.Stat(file.path)
		changed := statErr != nil || !sameHistoryFile(file.info, info)
		file.mu.Unlock()
		if !changed {
			return err
		}
		c.mu.Lock()
		// An older in-flight reader must not evict a newer file generation.
		if c.files[file.key] == file {
			c.removeFile(file)
		}
		c.mu.Unlock()
		if statErr != nil {
			return statErr
		}
	}
	return errHistoryChanged
}

// The caller holds file.mu. Cached slices never escape into mutable responses.
func (c *nativeHistoryCache) transcripts(file *nativeHistory) ([]chatmodel.Transcript, error) {
	c.mu.Lock()
	if cached := file.parts; cached != nil {
		c.mu.Unlock()
		return cloneTranscripts(cached), nil
	}
	c.mu.Unlock()
	read := c.readTurn
	if read == nil {
		read = agent.ReadSessionTurn
	}
	transcripts := make([]chatmodel.Transcript, len(file.index))
	bytes := 0
	for i, turn := range file.index {
		transcript, err := read(file.backend, file.path, turn)
		if err != nil {
			return nil, err
		}
		transcripts[i] = transcript
		bytes += 128
		for _, part := range transcript.Parts {
			bytes += 128 + len(part.Role) + len(part.Kind) + len(part.Name) + len(part.Model) + len(part.Text) + len(part.ImageURL)
		}
	}
	c.mu.Lock()
	limit := c.maxBytes
	if limit <= 0 {
		limit = historyCacheBytes
	}
	if c.files[file.key] != file {
		c.mu.Unlock()
		return transcripts, nil
	}
	// Retain an entire session together. Evicting its early turns while filling
	// a large snapshot would make every subsequent full read decode it again.
	c.fileLRU.MoveToFront(file.lru)
	for c.bytes+bytes > limit && c.fileLRU.Len() > 1 {
		c.removeFile(c.fileLRU.Back().Value.(*nativeHistory))
	}
	file.parts, file.bytes = transcripts, bytes
	c.bytes += bytes
	c.mu.Unlock()
	return cloneTranscripts(transcripts), nil
}

func cloneTranscripts(transcripts []chatmodel.Transcript) []chatmodel.Transcript {
	clones := make([]chatmodel.Transcript, len(transcripts))
	for i, transcript := range transcripts {
		clones[i] = cloneTranscript(transcript)
	}
	return clones
}

// The caller holds c.mu for all LRU and cache membership changes.
func (c *nativeHistoryCache) removeFile(file *nativeHistory) {
	c.bytes -= file.bytes
	file.parts, file.bytes = nil, 0
	delete(c.files, file.key)
	c.fileLRU.Remove(file.lru)
}

func (c *nativeHistoryCache) setMaxBytes(limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBytes = limit
	for c.bytes > limit && c.fileLRU.Len() > 1 {
		c.removeFile(c.fileLRU.Back().Value.(*nativeHistory))
	}
}

func (c *nativeHistoryCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, file := range c.files {
		c.removeFile(file)
	}
}

func (s *Store) indexHistory(backend chatmodel.Backend, path string) ([]agent.NativeTurn, error) {
	var turns []agent.NativeTurn
	err := s.historyCache.read(backend, path, func(file *nativeHistory) error {
		turns = file.index
		return nil
	})
	return turns, err
}
