//go:build linux && (amd64 || arm64)

package screencapture

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type screenshotPortalFixture struct {
	conn   *dbus.Conn
	uri    string
	code   uint32
	wait   bool
	called chan map[string]dbus.Variant
	closed chan struct{}
}
type screenshotPortalRequest struct{ closed chan struct{} }

func (r *screenshotPortalRequest) Close() *dbus.Error { r.closed <- struct{}{}; return nil }

func (p *screenshotPortalFixture) Screenshot(sender dbus.Sender, parent string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := options["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_") + "/" + token)
	if err := p.conn.Export(&screenshotPortalRequest{p.closed}, path, portalRequestInterface); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	p.called <- options
	if !p.wait {
		// Deliberately emit before returning: real portals may answer this fast.
		if err := p.conn.Emit(path, portalRequestInterface+".Response", p.code, map[string]dbus.Variant{"uri": dbus.MakeVariant(p.uri)}); err != nil {
			return "", dbus.MakeFailedError(err)
		}
	}
	return path, nil
}

func TestPortalScreenshotResponseAndCancellation(t *testing.T) {
	if os.Getenv("SPOOL_SCREENSHOT_PORTAL_FIXTURE") != "1" {
		t.Skip("requires an owned dbus-run-session")
	}
	service, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if reply, err := service.RequestName(portalDestination, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("fixture portal name: %v, %v", reply, err)
	}
	path := filepath.Join(t.TempDir(), "picked.png")
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 7, 5))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pngData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		code uint32
		wait bool
		want error
	}{
		{"early response", 0, false, nil}, {"user cancel", 1, false, context.Canceled}, {"portal failure", 2, false, ErrFailed}, {"timeout closes request", 0, true, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &screenshotPortalFixture{conn: service, uri: (&url.URL{Scheme: "file", Path: path}).String(), code: tc.code, wait: tc.wait, called: make(chan map[string]dbus.Variant, 1), closed: make(chan struct{}, 1)}
			if err := service.Export(fixture, "/org/freedesktop/portal/desktop", "org.freedesktop.portal.Screenshot"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			data, err := portalScreenshot(ctx)
			if err != tc.want {
				t.Fatalf("portal result %v, want %v", err, tc.want)
			}
			if tc.want == nil && !bytes.Equal(data, pngData.Bytes()) {
				t.Fatal("portal changed selected PNG")
			}
			select {
			case options := <-fixture.called:
				if options["interactive"].Value() != true || options["modal"].Value() != true {
					t.Fatal("portal must require explicit user selection")
				}
			default:
				t.Fatal("portal was not called")
			}
			select {
			case <-fixture.closed:
			case <-time.After(time.Second):
				t.Fatal("request not closed before capture returned")
			}
		})
	}
}
