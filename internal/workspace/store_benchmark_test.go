package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkStoreInitialization compares complete, isolated store lifecycles.
// Both paths retain OpenStore's connection settings and migration checks.
func BenchmarkStoreInitialization(b *testing.B) {
	if _, err := testStoreSchema(); err != nil {
		b.Fatal(err)
	}
	for _, copySchema := range []bool{false, true} {
		name := "fresh"
		open := OpenStore
		if copySchema {
			name = "copy-schema-and-open"
			open = openTestStore
		}
		b.Run(name, func(b *testing.B) {
			root := filepath.Join(b.TempDir(), "store")
			for b.Loop() {
				store, err := open(root)
				if err != nil {
					b.Fatal(err)
				}
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
				if err := os.RemoveAll(root); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
