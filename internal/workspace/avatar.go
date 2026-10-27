package workspace

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

// An empty seed follows the display name; a selected seed survives renames.
// Store only DiceBear options, never user-supplied SVG or external image URLs.
type Avatar struct {
	Style string `json:"style"`
	Seed  string `json:"seed"`
}

func DefaultAvatar() Avatar { return Avatar{Style: "clay"} }

func (v Avatar) Validate() error {
	switch v.Style {
	case "clay", "pixel-art", "croodles", "lorelei", "notionists", "toon-head", "voxel-art", "thumbs":
	default:
		return fmt.Errorf("Invalid avatar style")
	}
	if len(v.Seed) > 128 || !utf8.ValidString(v.Seed) || strings.IndexFunc(v.Seed, unicode.IsControl) >= 0 || (v.Seed != "" && strings.TrimSpace(v.Seed) == "") {
		return fmt.Errorf("Invalid avatar seed")
	}
	return nil
}

func (s *Store) SaveAvatar(user string, v Avatar) error {
	if !chatmodel.ValidID(user) {
		return fmt.Errorf("invalid user ID")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO user_avatars(user_id,style,seed)
		SELECT id,?,? FROM users WHERE id=? AND disabled=0
		ON CONFLICT(user_id) DO UPDATE SET style=excluded.style,seed=excluded.seed`, v.Style, v.Seed, user)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return fmt.Errorf("user not found or disabled")
	}
	return nil
}
