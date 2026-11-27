package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadDroppedAttachmentsPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "笔记.txt")
	image := filepath.Join(dir, "preview")
	text := []byte("# Notes\n保留原始内容")
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16))
	for path, data := range map[string][]byte{notes: text, image: png} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	modified := time.UnixMilli(1700000000123)
	if err := os.Chtimes(notes, modified, modified); err != nil {
		t.Fatal(err)
	}
	files, err := readDroppedAttachments([]string{notes, image})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "笔记.txt" || files[0].Type != "text/plain" ||
		!bytes.Equal(files[0].Data, text) || files[0].Modified != modified.UnixMilli() ||
		files[1].Name != "preview" || files[1].Type != "image/png" || !bytes.Equal(files[1].Data, png) {
		t.Fatalf("unexpected files: %+v", files)
	}
}

func TestReadDroppedAttachmentsRejectsInvalidBatches(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, bytes.Repeat([]byte{42}, size), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good.txt", 10)
	empty := write("empty.txt", 0)
	large := write("large.txt", 4*1024*1024+1)
	max := write("max.txt", 4*1024*1024)
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"missing", []string{good, filepath.Join(dir, "missing.txt")}, "Could not read the dropped files"},
		{"directory", []string{good, dir}, "Could not read the dropped files"},
		{"relative", []string{"good.txt"}, "Could not read the dropped files"},
		{"empty", []string{good, empty}, "Each attachment must be nonempty and at most 4 MB"},
		{"oversize", []string{good, large}, "Each attachment must be nonempty and at most 4 MB"},
		{"count", []string{good, good, good, good, good, good, good}, "Attach at most 6 files, up to 16 MB total"},
		{"total", []string{max, max, max, max, good}, "Attach at most 6 files, up to 16 MB total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := readDroppedAttachments(tc.paths)
			if err == nil || err.Error() != tc.want || files != nil {
				t.Fatalf("got %d files, %v; want atomic rejection %q", len(files), err, tc.want)
			}
		})
	}
	if files, err := readDroppedAttachments([]string{max, max, max, max}); err != nil || len(files) != 4 {
		t.Fatalf("exact total limit rejected: %d files, %v", len(files), err)
	}
}
