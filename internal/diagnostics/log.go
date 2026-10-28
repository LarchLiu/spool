// Package diagnostics persists application logs and Go crash reports independently
// of SQLite, so startup failures remain diagnosable.
package diagnostics

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
)

const maxLogBytes = 1 << 20

type Session struct {
	Path, CrashPath, PreviousCrashPath string
	Warning                            error
	writer                             *rotatingWriter
	previousOutput                     io.Writer
	previousFlags                      int
	previousPrefix                     string
}

// Start captures the standard logger before services or the GUI are initialized.
// If the data root is unusable, logs fall back to the user cache, then a private
// temporary directory. Crash output stays installed even as panic defers unwind.
func Start(root, component, version string, development bool) (*Session, error) {
	if component != "desktop" && component != "server" {
		return nil, fmt.Errorf("invalid diagnostics component %q", component)
	}
	if version == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			version = info.Main.Version
		}
	}
	name := "spool"
	if development {
		name += "-dev"
	}
	directories := []string{filepath.Join(root, "log")}
	if cache, err := os.UserCacheDir(); err == nil {
		directories = append(directories, filepath.Join(cache, name, "log"))
	}
	var failures []error
	var session *Session
	for _, directory := range directories {
		var err error
		session, err = openSession(directory, component)
		if err == nil {
			break
		}
		failures = append(failures, err)
	}
	if session == nil {
		directory, err := os.MkdirTemp("", name+"-logs-")
		if err == nil {
			session, err = openSession(filepath.Join(directory, "log"), component)
		}
		if err != nil {
			return nil, errors.Join(append(failures, err)...)
		}
	}
	session.Warning = errors.Join(failures...)
	session.previousOutput, session.previousFlags, session.previousPrefix = log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(mirroredWriter{file: session.writer, console: session.previousOutput})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.SetPrefix(fmt.Sprintf("[%s pid=%d] ", component, os.Getpid()))
	log.Printf("event=process_started version=%q development=%t platform=%s/%s data_dir=%q log=%q", version, development, runtime.GOOS, runtime.GOARCH, root, session.Path)
	if session.Warning != nil {
		log.Printf("event=log_directory_fallback error=%q", session.Warning)
	}
	if session.PreviousCrashPath != "" {
		log.Printf("event=previous_crash_found path=%q", session.PreviousCrashPath)
	}
	if err := captureCrashes(session.CrashPath, component, version); err != nil {
		session.Warning = errors.Join(session.Warning, err)
		log.Printf("event=crash_capture_failed error=%q", err)
	}
	return session, nil
}

func openSession(directory, component string) (*Session, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, component+".log")
	file, err := privateLogFile(path)
	if err != nil {
		return nil, err
	}
	s := &Session{Path: path, CrashPath: filepath.Join(directory, component+"-crash.log"), writer: &rotatingWriter{path: path, file: file}}
	if info, err := os.Stat(s.CrashPath); err == nil && info.Size() != 0 {
		if err := rotate(s.CrashPath); err != nil {
			file.Close()
			return nil, err
		}
		s.PreviousCrashPath = s.CrashPath + ".1"
	}
	return s, nil
}

func (s *Session) Close() error {
	// Do not unset SetCrashOutput: a main-goroutine panic prints its crash report
	// only after this deferred cleanup has run.
	log.SetOutput(s.previousOutput)
	log.SetFlags(s.previousFlags)
	log.SetPrefix(s.previousPrefix)
	return s.writer.Close()
}

func privateLogFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type mirroredWriter struct{ file, console io.Writer }

func (w mirroredWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	// Windows GUI builds may have no valid stderr handle. Conversely a full
	// disk must not suppress stderr, so both destinations are always attempted.
	_, _ = w.console.Write(data)
	return n, err
}

func rotate(path string) error {
	if err := os.Remove(path + ".3"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := 2; i >= 0; i-- {
		from := path
		if i != 0 {
			from = fmt.Sprintf("%s.%d", path, i)
		}
		if err := os.Rename(from, fmt.Sprintf("%s.%d", path, i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

type rotatingWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	written := 0
	for len(data) != 0 {
		info, err := w.file.Stat()
		if err != nil {
			return written, err
		}
		if info.Size() >= maxLogBytes {
			if err := w.file.Close(); err != nil {
				return written, err
			}
			w.file = nil
			if err := rotate(w.path); err != nil {
				return written, err
			}
			w.file, err = privateLogFile(w.path)
			if err != nil {
				return written, err
			}
			continue
		}
		chunk := min(len(data), maxLogBytes-int(info.Size()))
		n, err := w.file.Write(data[:chunk])
		written += n
		if err != nil {
			return written, err
		}
		data = data[n:]
	}
	return written, w.file.Sync()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
