package avatar

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestExactWebSVGParity(t *testing.T) {
	data, err := os.ReadFile("testdata/web-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Seed, SHA256, SVG string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 24 {
		t.Fatal("all eight styles and Unicode seeds need Web parity fixtures")
	}
	renderer := NewRenderer()
	for _, test := range cases {
		t.Run(test.Name+"/"+test.Seed, func(t *testing.T) {
			image, err := renderer.Render(test.Name, test.Seed)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256([]byte(image.SVG))); got != test.SHA256 {
				t.Fatalf("SVG differs from pinned Web generator: got %s, want %s", got, test.SHA256)
			}
			if image.SVG != test.SVG {
				t.Fatal("Go, Web, and native rendering fixture must use identical SVG")
			}
			if !strings.Contains(image.SVG, "<metadata") || !strings.HasPrefix(image.SVG, "<svg ") {
				t.Fatal("missing SVG or original license metadata")
			}
		})
	}
}

func TestOptionsAndExecutableInput(t *testing.T) {
	renderer := NewRenderer()
	for _, input := range []key{{"https://example.com/x", "seed"}, {"../clay", "x"},
		{"initials", "x"}, {"clay", strings.Repeat("x", 513)}, {"clay", "a\nb"}, {"clay", "\xff"}} {
		if _, err := renderer.Render(input.style, input.seed); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("accepted unsafe options: %#v: %v", input, err)
		}
	}
	for style := range definitions {
		image, err := renderer.Render(style, `<script onload="track()"> & </svg><foreignObject>`)
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range []string{"<script", "<foreignObject", "<image", "onload="} {
			if strings.Contains(image.SVG, tag) {
				t.Fatalf("%s allowed seed markup: %s", style, tag)
			}
		}
	}
}

func TestBoundedConcurrentCache(t *testing.T) {
	renderer := NewRenderer()
	renderer.maxEntries = 3
	renderer.maxBytes = 24 << 10
	var workers sync.WaitGroup
	for i := range 24 {
		workers.Go(func() {
			image, err := renderer.Render("clay", fmt.Sprint(i%6))
			if err != nil || image.ETag == "" {
				t.Errorf("render failed: %v", err)
			}
		})
	}
	workers.Wait()
	if len(renderer.cache) > 3 || renderer.bytes > renderer.maxBytes {
		t.Fatal("cache exceeded budget")
	}
	first, err := renderer.Render("thumbs", "stable")
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderer.Render("thumbs", "stable")
	if err != nil || first != second {
		t.Fatalf("cache changed rendered image: %v", err)
	}
}
