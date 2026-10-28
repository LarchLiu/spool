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
	partLRU  list.List
	bytes    int
	maxFiles int
	maxBytes int
	index    func(chatmodel.Backend, string, int64, int64) ([]agent.NativeTurn, error)
	readTurn func(chatmodel.Backend, string, agent.NativeTurn) (chatmodel.Transcript, error)
}

type nativeHistory struct {
	mu      sync.Mutex
	key     string
	backend chatmodel.Backend
	path    string
	info    os.FileInfo
	indexed bool
	index   []agent.NativeTurn
	parts   map[int]*cachedNativeTranscript
	lru     *list.Element
}

type cachedNativeTranscript struct {
	owner      *nativeHistory
	turn       int
	transcript chatmodel.Transcript
	bytes      int
	lru        *list.Element
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
	file := &nativeHistory{key: key, backend: backend, path: path, info: info, parts: make(map[int]*cachedNativeTranscript)}
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
				index = agent.IndexSessionTurnsTo
			}
			file.index, err = index(backend, file.path, 0, file.info.Size())
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
func (c *nativeHistoryCache) transcript(file *nativeHistory, turn int) (chatmodel.Transcript, error) {
	c.mu.Lock()
	if cached := file.parts[turn]; cached != nil {
		c.partLRU.MoveToFront(cached.lru)
		transcript := cloneTranscript(cached.transcript)
		c.mu.Unlock()
		return transcript, nil
	}
	c.mu.Unlock()
	read := c.readTurn
	if read == nil {
		read = agent.ReadSessionTurn
	}
	transcript, err := read(file.backend, file.path, file.index[turn])
	if err != nil {
		return transcript, err
	}
	bytes := 128
	for _, part := range transcript.Parts {
		bytes += 128 + len(part.Role) + len(part.Kind) + len(part.Name) + len(part.Model) + len(part.Text) + len(part.ImageURL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := c.maxBytes
	if limit <= 0 {
		limit = historyCacheBytes
	}
	if bytes > limit || c.files[file.key] != file {
		return transcript, nil
	}
	for c.bytes+bytes > limit {
		c.removeTranscript(c.partLRU.Back().Value.(*cachedNativeTranscript))
	}
	cached := &cachedNativeTranscript{owner: file, turn: turn, transcript: cloneTranscript(transcript), bytes: bytes}
	cached.lru = c.partLRU.PushFront(cached)
	file.parts[turn] = cached
	c.bytes += bytes
	return transcript, nil
}

// The caller holds c.mu for all LRU and cache membership changes.
func (c *nativeHistoryCache) removeTranscript(cached *cachedNativeTranscript) {
	delete(cached.owner.parts, cached.turn)
	c.partLRU.Remove(cached.lru)
	c.bytes -= cached.bytes
}

func (c *nativeHistoryCache) removeFile(file *nativeHistory) {
	for _, cached := range file.parts {
		c.removeTranscript(cached)
	}
	delete(c.files, file.key)
	c.fileLRU.Remove(file.lru)
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
