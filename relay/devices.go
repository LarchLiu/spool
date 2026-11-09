package relay

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const PairingLifetime = 5 * time.Minute
const devicePairPath = "/_spool/pair"

type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey []byte `json:"key"`
	CreatedAt int64  `json:"created_at"`
}
type DeviceRequest struct {
	Device
	RequestID string `json:"request_id"`
	ExpiresAt int64  `json:"expires_at"`
}
type DeviceSnapshot struct {
	Devices []Device        `json:"devices"`
	Pending []DeviceRequest `json:"pending"`
}
type deviceInvitation struct {
	token   string
	expires time.Time
	request *DeviceRequest
}
type deviceApproval struct {
	token   string
	expires time.Time
}

// Devices belongs to the home server, never the VPS. Only approved public keys
// persist. Invitations and pending requests disappear on service restart.
type Devices struct {
	mu          sync.Mutex
	path        string
	keys        map[string]Device
	invitation  *deviceInvitation
	approvals   map[string]deviceApproval
	connections map[*deviceConn]string
}

func OpenDevices(directory string) (*Devices, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	d := &Devices{path: filepath.Join(directory, "devices.json"), keys: map[string]Device{}, approvals: map[string]deviceApproval{}, connections: map[*deviceConn]string{}}
	b, err := os.ReadFile(d.path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var saved []Device
	if json.Unmarshal(b, &saved) != nil || len(saved) > 256 {
		return nil, errors.New("invalid paired device storage")
	}
	for _, device := range saved {
		if len(device.PublicKey) != 32 || DeviceFingerprint(device.PublicKey) != device.ID || !validDeviceName(device.Name) {
			return nil, errors.New("invalid paired device storage")
		}
		d.keys[device.ID] = device
	}
	return d, nil
}

func validDeviceName(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 128 && strings.IndexFunc(name, unicode.IsControl) == -1
}

func (d *Devices) saveLocked() error {
	devices := make([]Device, 0, len(d.keys))
	for _, device := range d.keys {
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	b, err := json.Marshal(devices)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(d.path), "devices-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
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
	return os.Rename(f.Name(), d.path)
}

func (d *Devices) Invite() (string, int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, approval := range d.approvals {
		if !time.Now().Before(approval.expires) {
			delete(d.approvals, id)
		}
	}
	d.invitation = &deviceInvitation{token: randomID(), expires: time.Now().Add(PairingLifetime)}
	return d.invitation.token, d.invitation.expires.Unix()
}

func (d *Devices) CancelInvitation(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.invitation != nil && (token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(d.invitation.token)) == 1) {
		d.invitation = nil
	}
}

func (d *Devices) Snapshot() DeviceSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := DeviceSnapshot{Devices: []Device{}, Pending: []DeviceRequest{}}
	for _, device := range d.keys {
		s.Devices = append(s.Devices, device)
	}
	sort.Slice(s.Devices, func(i, j int) bool { return s.Devices[i].CreatedAt < s.Devices[j].CreatedAt })
	if p := d.invitation; p != nil && time.Now().Before(p.expires) && p.request != nil {
		s.Pending = append(s.Pending, *p.request)
	}
	return s
}

func (d *Devices) Decide(requestID string, approve bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.invitation
	if p == nil || p.request == nil || p.request.RequestID != requestID || !time.Now().Before(p.expires) {
		return errors.New("Pairing request expired. Scan a new code.")
	}
	if !approve {
		d.invitation = nil
		return nil
	}
	if len(d.keys) >= 256 {
		return errors.New("Remove an unused device before pairing another.")
	}
	device := p.request.Device
	previous, existed := d.keys[device.ID]
	d.keys[device.ID] = device
	if err := d.saveLocked(); err != nil {
		if existed {
			d.keys[device.ID] = previous
		} else {
			delete(d.keys, device.ID)
		}
		return err
	}
	// Keep the result bound to this key until the original expiry. Closing the
	// pairing UI or inviting another phone must not discard an unread approval.
	d.approvals[device.ID] = deviceApproval{token: p.token, expires: p.expires}
	d.invitation = nil
	return nil
}

func (d *Devices) Revoke(id string) error {
	d.mu.Lock()
	device, ok := d.keys[id]
	if !ok {
		d.mu.Unlock()
		return errors.New("Paired device not found.")
	}
	delete(d.keys, id)
	if err := d.saveLocked(); err != nil {
		d.keys[id] = device
		d.mu.Unlock()
		return err
	}
	if p := d.invitation; p != nil && p.request != nil && p.request.ID == id {
		d.invitation = nil
	}
	delete(d.approvals, id)
	var closeList []*deviceConn
	for conn, key := range d.connections {
		if key == id {
			closeList = append(closeList, conn)
		}
	}
	d.mu.Unlock()
	for _, conn := range closeList {
		conn.Close()
	}
	return nil
}

// Listener selects client-certificate policy before the handshake. The Web
// exception is available only on the outbound relay, never the LAN listener.
func (d *Devices) Listener(base net.Listener, config *tls.Config, serverID string, webAllowed func() bool, web bool) net.Listener {
	return &deviceListener{Listener: base, devices: d, config: config, serverID: serverID, webAllowed: webAllowed, web: web}
}

type deviceListener struct {
	net.Listener
	devices    *Devices
	config     *tls.Config
	serverID   string
	webAllowed func() bool
	web        bool
}
type deviceConn struct {
	net.Conn
	devices *Devices
	once    sync.Once
}

func (c *deviceConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.devices.mu.Lock(); delete(c.devices.connections, c); c.devices.mu.Unlock() })
	return err
}
func (l *deviceListener) Accept() (net.Conn, error) {
	raw, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	c := &deviceConn{Conn: raw, devices: l.devices}
	d := l.devices
	d.mu.Lock()
	d.connections[c] = ""
	d.mu.Unlock()
	cfg := l.config.Clone()
	cfg.SessionTicketsDisabled = true
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		selected := cfg.Clone()
		selected.GetConfigForClient = nil
		name := hello.ServerName
		if name == l.serverID+".spool.invalid" && l.web && l.webAllowed() {
			return selected, nil
		}
		if name != DeviceServerName(l.serverID) && name != PairingServerName(l.serverID) {
			return nil, errors.New("paired device required")
		}
		selected.ClientAuth = tls.RequireAnyClientCert
		selected.VerifyConnection = func(state tls.ConnectionState) error {
			key, err := DevicePublicKey(state)
			if err != nil {
				return err
			}
			id := DeviceFingerprint(key)
			d.mu.Lock()
			defer d.mu.Unlock()
			if name == DeviceServerName(l.serverID) {
				if _, ok := d.keys[id]; !ok {
					return errors.New("device is not paired or was revoked")
				}
			}
			d.connections[c] = id
			return nil
		}
		return selected, nil
	}
	return tls.Server(c, cfg), nil
}

// Handler confines unapproved keys to pairing, and Web clients to the locally
// enabled path. Neither HTTP headers nor a relay-supplied flag grant access.
func (d *Devices) Handler(next http.Handler, serverID string, webAllowed func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil {
			http.Error(w, "device authentication required", 403)
			return
		}
		switch r.TLS.ServerName {
		case PairingServerName(serverID):
			d.pair(w, r)
		case DeviceServerName(serverID):
			key, err := DevicePublicKey(*r.TLS)
			if err != nil {
				http.Error(w, "device authentication required", 403)
				return
			}
			d.mu.Lock()
			_, ok := d.keys[DeviceFingerprint(key)]
			d.mu.Unlock()
			if !ok {
				http.Error(w, "device revoked; pair again", 403)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/web/") {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		case serverID + ".spool.invalid":
			if !webAllowed() || !strings.HasPrefix(r.URL.Path, WebPath(serverID)) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "device authentication required", 403)
		}
	})
}

func (d *Devices) pair(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != devicePairPath || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	key, err := DevicePublicKey(*r.TLS)
	if err != nil {
		http.Error(w, "device certificate required", 403)
		return
	}
	var input struct {
		Invitation string `json:"invitation"`
		Name       string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || !validDeviceName(input.Name) {
		http.Error(w, "invalid pairing request", 400)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	id := DeviceFingerprint(key)
	if approval, ok := d.approvals[id]; ok {
		if !time.Now().Before(approval.expires) {
			delete(d.approvals, id)
		} else if subtle.ConstantTimeCompare([]byte(input.Invitation), []byte(approval.token)) == 1 {
			writePairingState(w, "approved", id)
			return
		}
	}
	p := d.invitation
	if p == nil || !time.Now().Before(p.expires) || subtle.ConstantTimeCompare([]byte(input.Invitation), []byte(p.token)) != 1 {
		http.Error(w, "Pairing invitation expired or canceled. Scan a new code.", 403)
		return
	}
	if p.request == nil {
		p.request = &DeviceRequest{Device: Device{ID: id, Name: strings.TrimSpace(input.Name), PublicKey: append([]byte(nil), key...), CreatedAt: time.Now().Unix()}, RequestID: randomID(), ExpiresAt: p.expires.Unix()}
	} else if p.request.ID != id {
		http.Error(w, "Pairing invitation already used. Scan a new code.", 403)
		return
	}
	writePairingState(w, "pending", id)
}

func writePairingState(w http.ResponseWriter, state, id string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"state": state, "device_id": id})
}

func (d *Devices) CloseConnections() {
	d.mu.Lock()
	connections := make([]*deviceConn, 0, len(d.connections))
	for c := range d.connections {
		connections = append(connections, c)
	}
	d.invitation = nil
	clear(d.approvals)
	d.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}

// PairDevice only talks to the pinned home identity; it never sends the
// invitation or device key to the relay HTTP API. Retrying is key-bound.
func PairDevice(ctx context.Context, client *http.Client, p Pairing, name string) (string, error) {
	b, err := json.Marshal(map[string]string{"invitation": p.Invitation, "name": name})
	if err != nil {
		return "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, Origin(p.ID())+devicePairPath, strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("Pairing invitation expired, rejected or already used. Scan a new code.")
	}
	var result struct {
		State string `json:"state"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2048)).Decode(&result) != nil || (result.State != "pending" && result.State != "approved") {
		return "", errors.New("invalid pairing response")
	}
	return result.State, nil
}
