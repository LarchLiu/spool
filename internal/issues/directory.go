package issues

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Directory keys shared state by the canonical main checkout, not an issue
// worktree. Replace each separator, punctuation mark and symbol individually.
func Directory(dataRoot, checkout string) string {
	key := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return '-'
	}, checkout)
	return filepath.Join(dataRoot, "projects", key, "issues")
}

func privateDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("issues state directory %s must not be a symlink", path)
	}
	return nil
}

func PrepareDirectory(dataRoot, checkout string) (string, error) {
	if !filepath.IsAbs(checkout) {
		return "", fmt.Errorf("issues state requires an absolute main checkout directory")
	}
	directory := Directory(dataRoot, checkout)
	project := filepath.Dir(directory)
	for _, path := range []string{filepath.Join(dataRoot, "projects"), project, directory} {
		if err := privateDirectory(path); err != nil {
			return "", err
		}
	}
	// Distinct paths can encode to the same name (e.g. a.b and a-b). Refuse
	// that collision instead of sharing unrelated issue numbers and claims.
	identity := filepath.Join(project, ".project-directory")
	if _, err := os.Lstat(identity); errors.Is(err, os.ErrNotExist) {
		// Publish a complete marker atomically: simultaneous label workers must
		// never mistake another worker's not-yet-written marker for a collision.
		f, err := os.CreateTemp(project, ".project-directory-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		_, writeErr := f.WriteString(checkout)
		closeErr := f.Close()
		if err = errors.Join(writeErr, closeErr); err != nil {
			return "", err
		}
		if err = os.Link(f.Name(), identity); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	body, err := readLedgerFile(identity)
	if err != nil {
		return "", err
	}
	if string(body) != checkout {
		return "", fmt.Errorf("issues state directory name collision for %s; refusing to use another project's records", checkout)
	}
	return directory, nil
}
