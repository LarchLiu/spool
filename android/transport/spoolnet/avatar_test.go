package spoolnet

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestLocalAvatarMatchesWebWithoutClientOrSession(t *testing.T) {
	data, err := os.ReadFile("../../../internal/avatar/testdata/web-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Seed, SVG, SHA256 string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatal("expected all eight styles with three shared Web seeds")
	}
	for _, test := range cases {
		t.Run(test.Name+"/"+test.Seed, func(t *testing.T) {
			image, err := RenderAvatar(test.Name, test.Seed)
			if err != nil {
				t.Fatal(err)
			}
			if image != test.SVG || fmt.Sprintf("%x", sha256.Sum256([]byte(image))) != test.SHA256 {
				t.Fatal("local Android bridge differs from the pinned Web SVG")
			}
		})
	}
}

func TestLocalAvatarRejectsUnsafeOptionsAndSupportsConcurrentCachedReads(t *testing.T) {
	for _, input := range [][2]string{{"unknown", "seed"}, {"clay", "a\nb"}, {"clay", strings.Repeat("x", 513)}} {
		if svg, err := RenderAvatar(input[0], input[1]); err == nil || svg != "" {
			t.Fatal("invalid local options must not produce an image")
		}
	}
	expected, err := RenderAvatar("thumbs", "shared:local")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			actual, err := RenderAvatar("thumbs", "shared:local")
			if err != nil || actual != expected {
				t.Errorf("concurrent local generation changed the SVG: %v", err)
			}
		})
	}
	workers.Wait()
}

func BenchmarkRenderAvatar(b *testing.B) {
	b.Run("cached", func(b *testing.B) {
		if _, err := RenderAvatar("clay", "benchmark:cached"); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, err := RenderAvatar("clay", "benchmark:cached"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("new-seed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := RenderAvatar("clay", fmt.Sprintf("benchmark:new:%d", i)); err != nil {
				b.Fatal(err)
			}
		}
	})
}
