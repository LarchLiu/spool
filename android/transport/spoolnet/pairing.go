package spoolnet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"spool/relay"
)

var deviceStorageMu sync.Mutex

type savedDevice struct {
	ServerID   string `json:"server_id"`
	Seed       []byte `json:"seed"`
	Invitation string `json:"invitation,omitempty"`
	Approved   bool   `json:"approved"`
}

func devicePath(directory, id string) string { return filepath.Join(directory, "devices", id+".json") }

func loadDevice(directory, id string) (savedDevice, error) {
	return readDevice(devicePath(directory, id), id)
}

func readDevice(path, id string) (savedDevice, error) {
	var device savedDevice
	b, err := os.ReadFile(path)
	if err != nil {
		return device, err
	}
	if json.Unmarshal(b, &device) != nil || device.ServerID != id || len(device.Seed) != ed25519.SeedSize {
		return savedDevice{}, errors.New("invalid saved device identity; pair again")
	}
	return device, nil
}

func saveDevice(directory string, device savedDevice) error {
	path := devicePath(directory, device.ServerID)
	if !device.Approved {
		path += ".pending"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "device-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	b, err := json.Marshal(device)
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// PairingSession owns a cancelable, pinned connection while the desktop decides.
// No Spool account cookies or credentials are sent on this transport.
type PairingSession struct {
	mu        sync.Mutex
	directory string
	device    savedDevice
	pairing   relay.Pairing
	name      string
	ctx       context.Context
	cancel    context.CancelFunc
	dialer    *relay.Dialer
	transport *http.Transport
	outer     *http.Transport
	client    *http.Client
}

func BeginRelayPairing(directory, code, name string) (*PairingSession, error) {
	deviceStorageMu.Lock()
	defer deviceStorageMu.Unlock()
	if !filepath.IsAbs(directory) {
		return nil, errors.New("native state directory must be absolute")
	}
	p, err := relay.ParsePairing(code)
	if err != nil {
		return nil, err
	}
	if p.Invitation == "" || p.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("Pairing invitation expired. Scan a new code.")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Android phone"
	}
	device, err := readDevice(devicePath(directory, p.ID())+".pending", p.ID())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// Retry the same invitation with the same key, including after process death.
	// An approved/revoked identity is replaced only by an explicit new invitation.
	if err != nil || device.Invitation != p.Invitation {
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return nil, e
		}
		device = savedDevice{ServerID: p.ID(), Seed: key.Seed(), Invitation: p.Invitation}
		if e = saveDevice(directory, device); e != nil {
			return nil, e
		}
	}
	cfg, err := relay.DeviceTLSConfig(p, device.Seed, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	outer := http.DefaultTransport.(*http.Transport).Clone()
	outer.Proxy = nil
	dialer, err := relay.NewDialer(ctx, p, &http.Client{Transport: outer, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("relay redirects disabled") }})
	if err != nil {
		cancel()
		return nil, err
	}
	dialer.TLSConfig = cfg
	client, transport := nativeHTTPClient(nil)
	client.Jar = nil
	transport.DialTLSContext = dialer.DialTLSContext
	return &PairingSession{directory: directory, device: device, pairing: p, name: name, ctx: ctx, cancel: cancel, dialer: dialer, transport: transport, outer: outer, client: client}, nil
}

func (s *PairingSession) Fingerprint() string {
	return relay.DeviceFingerprint(ed25519.NewKeyFromSeed(s.device.Seed).Public().(ed25519.PublicKey))
}

// Poll returns pending or approved; only explicit desktop approval saves trust.
func (s *PairingSession) Poll() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	state, err := relay.PairDevice(ctx, s.client, s.pairing, s.name)
	if err != nil {
		return "", err
	}
	if state == "approved" {
		deviceStorageMu.Lock()
		defer deviceStorageMu.Unlock()
		pending, err := readDevice(devicePath(s.directory, s.device.ServerID)+".pending", s.device.ServerID)
		if err != nil || !bytes.Equal(pending.Seed, s.device.Seed) {
			return "", errors.New("pairing was replaced; use the latest invitation")
		}
		approved := s.device
		approved.Approved = true
		if err = saveDevice(s.directory, approved); err != nil {
			return "", err
		}
		s.device.Approved = true
	}
	return state, nil
}

func (s *PairingSession) Code() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.device.Approved {
		return "", errors.New("desktop approval required")
	}
	p := s.pairing
	p.Invitation, p.ExpiresAt = "", 0
	return p.Encode()
}
func (s *PairingSession) Close() {
	s.cancel()
	s.dialer.Close()
	s.transport.CloseIdleConnections()
	s.outer.CloseIdleConnections()
}
