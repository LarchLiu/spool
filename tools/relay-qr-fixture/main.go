// Generates an inert cross-platform QR fixture. All-zero public keys and tokens
// intentionally do not identify a real server or grant any application access.
package main

import (
	"encoding/base64"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/skip2/go-qrcode"
	"spool/relay"
)

func main() {
	output := flag.String("out", "", "owned diagnostic directory for a synthetic QR and text payload")
	flag.Parse()
	if *output == "" {
		log.Fatal("-out is required")
	}
	p := relay.Pairing{Version: 1, RelayURL: "https://relay.example.com", PublicKey: make([]byte, 32), AccessToken: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), LAN: []string{"192.168.1.20:18789"}}
	code, err := p.Encode()
	if err != nil {
		log.Fatal(err)
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(*output, 0755); err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*output, "pairing-synthetic.png"), png, 0644); err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*output, "pairing-synthetic.txt"), []byte(code), 0644); err != nil {
		log.Fatal(err)
	}
}
