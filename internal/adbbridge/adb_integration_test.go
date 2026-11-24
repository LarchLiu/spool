package adbbridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// privateADBServer starts an adb server only this test talks to, with its own
// port and key and without USB, emulator or network discovery. A developer's
// adb server and any phone connected to it are never touched.
func privateADBServer(t *testing.T) CLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("adb keeps its key outside HOME on Windows")
	}
	path, ok := Locate()
	if !ok {
		t.Skip("adb is not installed")
	}
	home := t.TempDir()
	isolated := []string{"HOME=" + home, "ADB_USB=0", "ADB_EMU=0", "ADB_MDNS=0", "ADB_VENDOR_KEYS="}
	for attempt := 1; ; attempt++ {
		reserved, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := strconv.Itoa(reserved.Addr().(*net.TCPAddr).Port)
		reserved.Close()
		// A foreground server stays this test's child instead of daemonizing. It
		// only accepts a bare port, which it binds to loopback.
		server := exec.Command(path, "nodaemon", "server")
		server.Env = append(append(os.Environ(), isolated...), "ADB_SERVER_SOCKET=tcp:"+port)
		log := &bytes.Buffer{}
		server.Stdout, server.Stderr = log, log
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() {
			_ = server.Wait()
			close(exited)
		}()
		stop := func() {
			_ = server.Process.Kill()
			<-exited
		}
		// adb clients treat a numeric host as remote and never start a daemon of
		// their own there, so a dead private server cannot leave one behind.
		cli := CLI{Path: path, Env: append(isolated, "ADB_SERVER_SOCKET=tcp:127.0.0.1:"+port)}
		if answers(cli, exited) {
			t.Cleanup(func() {
				stop()
				if t.Failed() {
					t.Logf("adb server log:\n%s", log)
				}
			})
			if _, err := os.Stat(filepath.Join(home, ".android", "adbkey")); err != nil {
				t.Fatalf("the private adb server does not keep its key in its own home: %v", err)
			}
			return cli
		}
		stop()
		// Another process may have taken the port between reserving and binding.
		if attempt == 3 {
			t.Fatalf("private adb server did not start:\n%s", log)
		}
	}
}

func answers(cli CLI, exited <-chan struct{}) bool {
	deadline := time.Now().Add(patience)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return false
		default:
		}
		if output, _ := cli.run(context.Background(), 2*time.Second, "", "devices"); strings.HasPrefix(output, "List of devices attached") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func runADB(t *testing.T, cli CLI, args ...string) string {
	t.Helper()
	output, err := cli.run(context.Background(), patience, "", args...)
	if err != nil {
		t.Logf("adb %s: %v", strings.Join(args, " "), err)
	}
	return output
}

func deviceLine(devices, serial string) string {
	for _, line := range strings.Split(devices, "\n") {
		if strings.HasPrefix(line, serial) {
			return line
		}
	}
	return ""
}

const (
	packetCNXN = 0x4e584e43
	packetAUTH = 0x48545541
	packetSTLS = 0x534c5453
	packetOPEN = 0x4e45504f
	packetOKAY = 0x59414b4f
	packetCLSE = 0x45534c43
	packetWRTE = 0x45545257

	authToken     = 1
	authSignature = 2
	authPublicKey = 3
)

type deviceSecurity int

const (
	// A device that trusts every host, like an emulator.
	trustsAnyHost deviceSecurity = iota
	// USB debugging: the phone's user must allow the host's key.
	asksItsUser
	// Wireless debugging on a phone that was paired with the host's key.
	wirelessPaired
	// Wireless debugging on a phone that has never seen the host's key.
	wirelessUnpaired
)

// fakeDevice is a phone's adbd behind a client stream. It implements just
// enough of the ADB protocol for the host to bring it online and run a shell
// command.
type fakeDevice struct {
	t        *testing.T
	socket   net.Conn
	link     io.ReadWriter
	security deviceSecurity
	allow    chan struct{} // closed when the phone's user allows the host's key
	asked    chan struct{} // closed once the phone shows its authorization dialog
	askOnce  sync.Once
	// Shell streams the device closes once the host acknowledged their output.
	closing map[uint32]uint32
	streams uint32
}

func serveFakeDevice(t *testing.T, stream *client, security deviceSecurity) *fakeDevice {
	t.Helper()
	d := &fakeDevice{t: t, socket: deviceSocket{stream}, security: security, allow: make(chan struct{}), asked: make(chan struct{}), closing: map[uint32]uint32{}}
	d.link = d.socket
	go d.run()
	return d
}

// deviceSocket is the client stream as the phone's adbd sees its transport.
type deviceSocket struct{ stream *client }

func (s deviceSocket) Read(p []byte) (int, error) {
	data, ok := s.stream.next(1, len(p), nil)
	if !ok {
		return 0, io.EOF
	}
	return copy(p, data), nil
}

func (s deviceSocket) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	if err := s.stream.conn.Write(ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (deviceSocket) Close() error                     { return nil }
func (deviceSocket) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (deviceSocket) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (deviceSocket) SetDeadline(time.Time) error      { return nil }
func (deviceSocket) SetReadDeadline(time.Time) error  { return nil }
func (deviceSocket) SetWriteDeadline(time.Time) error { return nil }

func (d *fakeDevice) send(command, arg0, arg1 uint32, payload []byte) {
	packet := make([]byte, 24+len(payload))
	var sum uint32
	for _, b := range payload {
		sum += uint32(b)
	}
	for i, value := range []uint32{command, arg0, arg1, uint32(len(payload)), sum, command ^ 0xffffffff} {
		binary.LittleEndian.PutUint32(packet[i*4:], value)
	}
	copy(packet[24:], payload)
	// A stream that already ended is the test's business, not the device's.
	_, _ = d.link.Write(packet)
}

func (d *fakeDevice) online() {
	d.send(packetCNXN, 0x01000001, 256<<10, []byte("device::ro.product.name=spool;ro.product.model=SpoolFake;ro.product.device=fake;features="))
}

// bufferedDeviceSocket keeps any bytes read ahead while recognizing the TLS
// upgrade available to the handshake.
type bufferedDeviceSocket struct {
	net.Conn
	reader *bufio.Reader
}

func (s bufferedDeviceSocket) Read(p []byte) (int, error) { return s.reader.Read(p) }

func (d *fakeDevice) run() {
	reader := bufio.NewReader(d.socket)
	secured := false
	upgrade := func() bool {
		conn := tls.Server(bufferedDeviceSocket{d.socket, reader}, d.wirelessDebugging())
		if err := conn.Handshake(); err != nil {
			// adbd drops a host whose key it was not paired with.
			if d.security == wirelessPaired {
				d.t.Logf("fake device TLS handshake: %v", err)
			}
			return false
		}
		d.link, reader, secured = conn, bufio.NewReader(conn), true
		d.online()
		return true
	}
	header := make([]byte, 24)
	for {
		// adb queues its STLS reply and starts TLS on separate threads. TLS
		// can win that race, leaving the reply to arrive inside TLS later.
		if !secured && (d.security == wirelessPaired || d.security == wirelessUnpaired) {
			head, err := reader.Peek(4)
			if err != nil {
				return
			}
			if isTLSClientHello(head) {
				if !upgrade() {
					return
				}
				continue
			}
		}
		if _, err := io.ReadFull(reader, header); err != nil {
			return
		}
		command := binary.LittleEndian.Uint32(header[0:])
		arg0 := binary.LittleEndian.Uint32(header[4:])
		arg1 := binary.LittleEndian.Uint32(header[8:])
		length := binary.LittleEndian.Uint32(header[12:])
		if length > 1<<20 || binary.LittleEndian.Uint32(header[20:]) != command^0xffffffff {
			d.t.Errorf("invalid ADB header: %x", header)
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		switch command {
		case packetCNXN:
			switch d.security {
			case trustsAnyHost:
				d.online()
			case asksItsUser:
				d.send(packetAUTH, authToken, 0, bytes.Repeat([]byte{7}, 20))
			default:
				d.send(packetSTLS, 0x01000000, 0, nil)
			}
		case packetSTLS:
			if !secured && !upgrade() {
				return
			}
		case packetAUTH:
			switch arg0 {
			case authSignature:
				// Signed by a key this phone has not authorized yet.
				d.send(packetAUTH, authToken, 0, bytes.Repeat([]byte{9}, 20))
			case authPublicKey:
				d.askOnce.Do(func() {
					close(d.asked)
					go func() {
						<-d.allow
						d.online()
					}()
				})
			}
		case packetOPEN:
			if strings.TrimRight(string(payload), "\x00") != "shell:echo spool" {
				d.send(packetCLSE, 0, arg0, nil)
				continue
			}
			d.streams++
			local := d.streams
			d.send(packetOKAY, local, arg0, nil)
			d.send(packetWRTE, local, arg0, []byte("spool\n"))
			d.closing[local] = arg0
		case packetOKAY:
			if remote, pending := d.closing[arg1]; pending {
				delete(d.closing, arg1)
				d.send(packetCLSE, arg1, remote, nil)
			}
		}
	}
}

// wirelessDebugging is adbd's side of the TLS upgrade: any certificate of its
// own, and a host certificate it accepts only from a paired key.
func (d *fakeDevice) wirelessDebugging() *tls.Config {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		d.t.Error(err)
		return nil
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		d.t.Error(err)
		return nil
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certificate}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error {
			if d.security != wirelessPaired {
				return errors.New("this phone was never paired with that key")
			}
			return nil
		},
	}
}

func expectDevicePacket(t *testing.T, link io.Reader, command uint32) []byte {
	t.Helper()
	header := make([]byte, 24)
	if _, err := io.ReadFull(link, header); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(header); got != command {
		t.Fatalf("device sent command %#x, want %#x", got, command)
	}
	length := binary.LittleEndian.Uint32(header[12:])
	if length > 1<<20 {
		t.Fatalf("device sent oversized payload: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(link, payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// Exercise both adb thread orderings without depending on the installed adb
// or the scheduler to put ClientHello ahead of the STLS reply.
func TestFakeWirelessDeviceTLSUpgrade(t *testing.T) {
	for _, tlsFirst := range []bool{false, true} {
		name := "STLS before ClientHello"
		if tlsFirst {
			name = "ClientHello before STLS"
		}
		t.Run(name, func(t *testing.T) {
			phone, host := net.Pipe()
			d := &fakeDevice{t: t, socket: phone, link: phone, security: wirelessPaired, closing: map[uint32]uint32{}}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer phone.Close()
				d.run()
			}()
			t.Cleanup(func() {
				host.Close()
				phone.Close()
				<-finished
			})
			if err := host.SetDeadline(time.Now().Add(patience)); err != nil {
				t.Fatal(err)
			}
			peer := &fakeDevice{link: host}
			peer.send(packetCNXN, 0x01000001, 1<<20, nil)
			expectDevicePacket(t, host, packetSTLS)
			if !tlsFirst {
				peer.send(packetSTLS, 0x01000000, 0, nil)
			}
			conn := tls.Client(host, &tls.Config{
				Certificates:       d.wirelessDebugging().Certificates,
				MinVersion:         tls.VersionTLS13,
				InsecureSkipVerify: true, // adb accepts any phone certificate.
				// Avoid session-ticket writes blocking on the unbuffered pipe.
				SessionTicketsDisabled: true,
			})
			if err := conn.Handshake(); err != nil {
				t.Fatal(err)
			}
			peer.link = conn
			expectDevicePacket(t, conn, packetCNXN)
			if tlsFirst {
				peer.send(packetSTLS, 0x01000000, 0, nil)
			}
			peer.send(packetOPEN, 1, 0, []byte("shell:echo spool\x00"))
			expectDevicePacket(t, conn, packetOKAY)
			if got := string(expectDevicePacket(t, conn, packetWRTE)); got != "spool\n" {
				t.Fatalf("shell output = %q, want %q", got, "spool\n")
			}
		})
	}
}

func expectShell(t *testing.T, cli CLI, serial string) {
	t.Helper()
	if got := runADB(t, cli, "-s", serial, "shell", "echo", "spool"); got != "spool" {
		t.Fatalf("adb shell through the bridge printed %q", got)
	}
}

// The installed adb drives fake phones through the bridge, which checks the
// assumptions about adb that the bridge is built on.
func TestInstalledADBReachesDevicesThroughTheBridge(t *testing.T) {
	cli := privateADBServer(t)
	bridge := func(t *testing.T) *harness {
		h := newHarness(t)
		h.manager = NewWith(cli)
		return h
	}

	t.Run("a device behaves like one on a cable", func(t *testing.T) {
		h := bridge(t)
		stream := h.attach("alice", TransportUSB, "R58M000001")
		serveFakeDevice(t, stream, trustsAnyHost)
		serial := stream.ready()
		stream.expectState("online")
		if line := deviceLine(runADB(t, cli, "devices", "-l"), serial); !strings.Contains(line, "device") || !strings.Contains(line, "model:SpoolFake") {
			t.Fatalf("adb lists the bridged device as %q", line)
		}
		expectShell(t, cli, serial)

		if err := stream.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
			t.Fatal(err)
		}
		if result := stream.result(); result.serial != serial {
			t.Fatalf("attachment ended as %+v", result)
		}
		if line := deviceLine(runADB(t, cli, "devices"), serial); line != "" {
			t.Fatalf("adb still lists the detached device: %q", line)
		}

		// The phone returns under the serial that scripts on the host already use.
		again := h.attach("alice", TransportUSB, "R58M000001")
		serveFakeDevice(t, again, trustsAnyHost)
		if got := again.ready(); got != serial {
			t.Fatalf("returning device got serial %s, want %s", got, serial)
		}
		again.expectState("online")
		expectShell(t, cli, serial)

		// Anyone on the host may drop the device; its client is told why.
		runADB(t, cli, "disconnect", serial)
		again.expectError("adb_closed")
	})

	t.Run("a phone on USB debugging is unauthorized until its user allows the key", func(t *testing.T) {
		h := bridge(t)
		stream := h.attach("alice", TransportUSB, "R58M000002")
		device := serveFakeDevice(t, stream, asksItsUser)
		serial := stream.ready()
		stream.expectState("unauthorized")
		select {
		case <-device.asked:
		case <-time.After(patience):
			t.Fatal("adb never offered its public key to the phone")
		}
		if line := deviceLine(runADB(t, cli, "devices"), serial); !strings.Contains(line, "unauthorized") {
			t.Fatalf("adb lists the waiting device as %q", line)
		}
		close(device.allow)
		stream.expectState("online")
		expectShell(t, cli, serial)
	})

	t.Run("a paired phone on wireless debugging comes online over TLS", func(t *testing.T) {
		h := bridge(t)
		stream := h.attach("alice", TransportWireless, "pixel")
		serveFakeDevice(t, stream, wirelessPaired)
		serial := stream.ready()
		stream.expectState("online")
		expectShell(t, cli, serial)
	})

	t.Run("an unpaired phone on wireless debugging is reported as unpaired", func(t *testing.T) {
		h := bridge(t)
		stream := h.attach("alice", TransportWireless, "pixel-unpaired")
		serveFakeDevice(t, stream, wirelessUnpaired)
		serial := stream.ready()
		stream.expectError("unpaired")
		stream.result()
		if line := deviceLine(runADB(t, cli, "devices"), serial); line != "" {
			t.Fatalf("adb still lists the refused device: %q", line)
		}
	})
}
