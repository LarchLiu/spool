// Package avatar renders the same bundled DiceBear styles and options as Spool Web.
// Rendering is entirely local to the server or Android device: no external
// avatar service receives seeds or profile data.
package avatar

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	dicebear "github.com/dicebear/dicebear-go/v11"
	"github.com/dicebear/styles/v10"
)

var ErrInvalidOptions = errors.New("invalid avatar options")

var definitions = map[string]*string{
	"clay": &styles.Clay, "pixel-art": &styles.PixelArt,
	"croodles": &styles.Croodles, "lorelei": &styles.Lorelei,
	"notionists": &styles.Notionists, "toon-head": &styles.ToonHead,
	"voxel-art": &styles.VoxelArt, "thumbs": &styles.Thumbs,
}

type key struct{ style, seed string }

// Image is immutable and safe to share between requests.
type Image struct {
	SVG  string
	ETag string
}

type entry struct {
	key key
	Image
}

// Renderer bounds both cached image count and bytes; compiled styles are reused.
type Renderer struct {
	mu         sync.Mutex
	styles     map[string]*dicebear.Style
	cache      map[key]*list.Element
	order      *list.List
	bytes      int
	maxEntries int
	maxBytes   int
}

func NewRenderer() *Renderer {
	return &Renderer{styles: make(map[string]*dicebear.Style), cache: make(map[key]*list.Element),
		order: list.New(), maxEntries: 256, maxBytes: 8 << 20}
}

func ValidOptions(style, seed string) bool {
	_, ok := definitions[style]
	// A selected seed is limited to 128 bytes by the profile API. Automatic seeds
	// follow names, so allow a complete Unicode display name here as well.
	return ok && len(seed) <= 512 && utf8.ValidString(seed) && strings.IndexFunc(seed, unicode.IsControl) < 0
}

func (r *Renderer) Render(style, seed string) (Image, error) {
	if !ValidOptions(style, seed) {
		return Image{}, ErrInvalidOptions
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{style, seed}
	if element := r.cache[k]; element != nil {
		r.order.MoveToFront(element)
		return element.Value.(entry).Image, nil
	}
	compiled := r.styles[style]
	if compiled == nil {
		var err error
		compiled, err = dicebear.NewStyle([]byte(*definitions[style]))
		if err != nil {
			return Image{}, fmt.Errorf("compile bundled avatar style: %w", err)
		}
		r.styles[style] = compiled
	}
	generated, err := dicebear.NewAvatar(compiled, map[string]any{
		"seed": seed, "size": 96,
		"backgroundColor": []string{"dbe8d2", "f2d5c8", "e8dfb4", "cce2e5", "d8dded"},
	})
	if err != nil {
		return Image{}, fmt.Errorf("render bundled avatar: %w", err)
	}
	image := Image{SVG: generated.SVG()}
	if len(image.SVG) > 256<<10 {
		return Image{}, errors.New("bundled avatar exceeded its size budget")
	}
	image.ETag = fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(image.SVG)))
	r.cache[k] = r.order.PushFront(entry{k, image})
	r.bytes += len(image.SVG)
	for r.order.Len() > r.maxEntries || r.bytes > r.maxBytes {
		old := r.order.Back()
		value := old.Value.(entry)
		r.bytes -= len(value.SVG)
		delete(r.cache, value.key)
		r.order.Remove(old)
	}
	return image, nil
}
