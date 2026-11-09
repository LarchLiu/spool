package workspace

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestAddressQRCode(t *testing.T) {
	for _, address := range []string{"https://spool.example.test", "http://127.0.0.1:8787", "https://relay.example.test/web/server-id/?next=%2Fchat#home"} {
		encoded, err := AddressQRCode(address)
		if err != nil {
			t.Fatal(address, err)
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "data:image/png;base64,"))
		if err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil || image.Bounds().Dx() != 512 || image.Bounds().Dy() != 512 {
			t.Fatal("QR code is not a 512px PNG", err)
		}
	}
	for _, address := range []string{"", "spool.example.test", "javascript:alert(1)", "https://", "https://user:password@example.test", "https://example.test/\ninvalid"} {
		if _, err := AddressQRCode(address); err == nil {
			t.Fatal("accepted invalid browser address", address)
		}
	}
}
