package model

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const (
	MaxMessageBytes = 128 * 1024
)

func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}

func ValidID(id string) bool {
	if len(id) < 8 || len(id) > 128 || strings.HasPrefix(id, "-") {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func Clip(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && !isUTF8Boundary(text, end) {
		end--
	}
	return text[:end] + "\n…"
}

func isUTF8Boundary(text string, index int) bool {
	return index == 0 || index >= len(text) || text[index]&0xc0 != 0x80
}

func AppendBounded(current, add string, limit int) string {
	remaining := limit - len(current)
	if remaining <= 0 {
		return current
	}
	if len(add) > remaining {
		add = Clip(add, remaining)
	}
	return current + add
}
