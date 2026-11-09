package workspace

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// AddressQRCode encodes a browser URL locally, without contacting that address.
func AddressQRCode(address string) (string, error) {
	address = strings.TrimSpace(address)
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("Enter a valid HTTP or HTTPS address to show its QR code.")
	}
	png, err := qrcode.Encode(address, qrcode.Medium, 512)
	if err != nil {
		return "", fmt.Errorf("Could not generate the address QR code.")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
