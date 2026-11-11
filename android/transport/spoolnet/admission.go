package spoolnet

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"spool/relay"
)

// keepAdmissionCurrent collects the server's relay admission token after it
// rotates, which happens when another phone is revoked. The previous token
// only works for a grace period, so each wake-up asks once: when the client
// starts and whenever the network changes. A failed attempt waits for the
// next wake-up instead of polling an unreachable server.
func (c *Client) keepAdmissionCurrent(directory string, p relay.Pairing, seed []byte) {
	defer close(c.admissionDone)
	// No session cookies: the device certificate alone authorizes this.
	client := &http.Client{Transport: c.transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects are not allowed")
	}}
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.admissionSync:
		}
		ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		_ = syncAdmission(ctx, client, c.relay, directory, p, seed)
		cancel()
	}
}

// syncAdmission saves a new token before using it, then reports it back so
// the server can stop honoring the replaced one.
func syncAdmission(ctx context.Context, client *http.Client, dialer *relay.Dialer, directory string, p relay.Pairing, seed []byte) error {
	have := dialer.AccessToken()
	token, err := relay.SyncAdmission(ctx, client, p, have)
	if err != nil || token == have {
		return err
	}
	if err := saveAdmission(directory, p.ID(), seed, token); err != nil {
		return err
	}
	if err := dialer.SetAccessToken(token); err != nil {
		return err
	}
	_, err = relay.SyncAdmission(ctx, client, p, token)
	return err
}

func saveAdmission(directory, id string, seed []byte, token string) error {
	deviceStorageMu.Lock()
	defer deviceStorageMu.Unlock()
	device, err := loadDevice(directory, id)
	if err != nil {
		return err
	}
	// A later pairing owns this file, and its code carries its own token.
	if !device.Approved || !bytes.Equal(device.Seed, seed) {
		return errors.New("pairing was replaced")
	}
	device.AccessToken = token
	return saveDevice(directory, device)
}

func (c *Client) requestAdmissionSync() {
	if c.admissionSync == nil {
		return
	}
	select {
	case c.admissionSync <- struct{}{}:
	default:
	}
}
