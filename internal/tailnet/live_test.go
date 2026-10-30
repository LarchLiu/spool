package tailnet

import (
	"context"
	"io"
	"log"
	"net/url"
	"os"
	"testing"
	"time"

	"spool/internal/netproxy"
)

// Opt-in connectivity probe: it requests a link but never authorizes a node.
// State stays in t.TempDir and no login token or credentials are printed.
func TestTailnetLiveLoginURLThroughSystemProxy(t *testing.T) {
	if os.Getenv("SPOOL_TSNET_LOGIN_PROBE") != "1" {
		t.Skip("set SPOOL_TSNET_LOGIN_PROBE=1 for the isolated network probe")
	}
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE", "TS_CONTROL_URL"} {
		t.Setenv(key, "")
	}
	before, err := netproxy.Prepare(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netproxy.Activate(before) })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	policy, err := netproxy.Prepare(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	netproxy.Activate(policy)
	output := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(output) })
	endpoint, err := Start(ctx, t.TempDir(), Options{Hostname: "spool-login-probe", Port: 443}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	address, err := endpoint.LoginURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !IsLoginURL(address) {
		t.Fatal("no trusted node login link returned")
	}
	parsed, _ := url.Parse(address)
	t.Logf("login link received: host=%s; node authorization not performed", parsed.Hostname())
}
