package spoolnet

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestSessionClientsShareDeviceAndKeepIndependentPersistentLogins(t *testing.T) {
	const address = "https://target.example.test"
	device, err := NewDirectClient(t.TempDir(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { device.Close() })
	root := t.TempDir()
	open := func(name string) *Client {
		t.Helper()
		c, err := device.NewSessionClient(filepath.Join(root, name), address, "", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	ordinary, a, d := open("ordinary"), open("source-a"), open("source-d")
	for name, c := range map[string]*Client{"ordinary": ordinary, "source-a": a, "source-d": d} {
		if c.transport != device.transport || c.transportOwner != device {
			t.Fatal("session created another device connection")
		}
		c.http.Jar.SetCookies(c.origin, []*http.Cookie{sessionCookie(name)})
	}
	assertCookie := func(c *Client, want string) {
		t.Helper()
		cookies := c.http.Jar.Cookies(c.origin)
		if len(cookies) != 1 || cookies[0].Value != want {
			t.Fatalf("cookie = %v, want %s", cookies, want)
		}
	}
	a.Close()
	if device.ctx.Err() != nil || ordinary.ctx.Err() != nil || d.ctx.Err() != nil {
		t.Fatal("closing one login cancelled another login or its device")
	}
	assertCookie(ordinary, "ordinary")
	assertCookie(d, "source-d")
	a = open("source-a")
	assertCookie(a, "source-a")
	a.http.Jar.SetCookies(a.origin, []*http.Cookie{{Name: "spool_session", Path: "/", MaxAge: -1}})
	assertCookie(ordinary, "ordinary")
	assertCookie(d, "source-d")
	if cookies := a.http.Jar.Cookies(a.origin); len(cookies) != 0 {
		t.Fatal("logout retained its own session")
	}
	otherAccount := open("source-a-other-account")
	if len(otherAccount.http.Jar.Cookies(otherAccount.origin)) != 0 {
		t.Fatal("new source account inherited a previous login")
	}
	device.Close()
	if ordinary.ctx.Err() == nil || d.ctx.Err() == nil {
		t.Fatal("closing the device did not cancel its views")
	}
	if _, err := device.NewSessionClient(filepath.Join(root, "late"), address, "", ""); err == nil {
		t.Fatal("closed device opened a login")
	}
}
