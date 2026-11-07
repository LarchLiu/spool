package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
)

type fakeHealthAPI struct {
	state string
	err   error
}

func TestDirectHealthRecoversDNSAndNetworkFailuresWithoutNodeState(t *testing.T) {
	for _, test := range []struct {
		err     error
		problem string
	}{
		{},
		{&net.DNSError{Err: "no such host", Name: "chat.example.com", IsNotFound: true}, "network"},
		{syscall.ENETUNREACH, "network"},
		{healthStatusError{503}, "server"},
		{&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "endpoint"},
	} {
		info := checkHealth(context.Background(), nil, func(context.Context) error { return test.err })
		if info.State != "Running" || info.AuthURL != "" || info.Problem != test.problem {
			t.Fatalf("direct health depended on a node or misclassified %v: %+v", test.err, info)
		}
	}
}

func (f fakeHealthAPI) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
		panic("status sample must have a deadline")
	}
	return &ipnstate.Status{BackendState: f.state, AuthURL: "https://login.tailscale.com/a/test"}, f.err
}

func TestHealthClassifiesRecoveryFromBackendAndHTTPSIndependently(t *testing.T) {
	for _, test := range []struct {
		name, state, problem string
		statusErr, probeErr  error
	}{
		{name: "healthy", state: "Running"},
		{name: "unknown path does not decide health", state: "Running"},
		{name: "temporary status failure but HTTPS works", statusErr: io.EOF},
		{name: "status and HTTPS both fail", statusErr: io.EOF, probeErr: io.EOF, problem: "status"},
		{name: "timeout", state: "Running", probeErr: context.DeadlineExceeded, problem: "network"},
		{name: "reset", state: "Running", probeErr: syscall.ECONNRESET, problem: "network"},
		{name: "refused service", state: "Running", probeErr: syscall.ECONNREFUSED, problem: "server"},
		{name: "missing peer", state: "Running", probeErr: errServerNotReady, problem: "network"},
		{name: "HTTPS service unavailable", state: "Running", probeErr: healthStatusError{503}, problem: "server"},
		{name: "forbidden", state: "Running", probeErr: healthStatusError{403}, problem: "endpoint"},
		{name: "wrong server", state: "Running", probeErr: healthStatusError{200}, problem: "endpoint"},
		{name: "certificate", state: "Running", probeErr: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, problem: "endpoint"},
		{name: "sign in", state: "NeedsLogin", problem: "login"},
		{name: "approval", state: "NeedsMachineAuth", problem: "approval"},
		{name: "stopped", state: "Stopped", problem: "starting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			probes := 0
			info := checkHealth(context.Background(), fakeHealthAPI{test.state, test.statusErr}, func(ctx context.Context) error {
				probes++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 8*time.Second {
					t.Error("HTTPS sample has no deadline")
				}
				return test.probeErr
			})
			if info.Problem != test.problem {
				t.Fatalf("problem=%q, want %q: %+v", info.Problem, test.problem, info)
			}
			wantProbes := 1
			if test.state != "Running" && test.statusErr == nil {
				wantProbes = 0
			}
			if probes != wantProbes {
				t.Fatalf("probes=%d, want %d", probes, wantProbes)
			}
			if test.problem == "" && info.HTTPS != "Verified" {
				t.Fatalf("healthy response did not verify HTTPS: %+v", info)
			}
		})
	}
}

func TestHealthUsesRealExactOriginGETAndNeverReplaysAnAPI(t *testing.T) {
	var calls int
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/healthz" || r.Host != "example.com" {
			t.Errorf("unexpected health request: %s %s %s", r.Method, r.URL, r.Host)
		}
		io.WriteString(w, `{"ok":true}`)
	})
	info := checkHealth(client.ctx, fakeHealthAPI{state: "Running"}, client.probeOnce)
	if info.HTTPS != "Verified" || info.Problem != "" || calls != 1 {
		t.Fatalf("invalid health sample: %+v, calls=%d", info, calls)
	}
	// Invalid TLS remains a surfaced endpoint problem, even if status is unavailable.
	client.transport.CloseIdleConnections()
	client.transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	info = checkHealth(client.ctx, fakeHealthAPI{err: errors.New("local status failed")}, client.probeOnce)
	if info.Problem != "endpoint" || calls != 1 {
		t.Fatalf("certificate error was retried or misclassified: %+v calls=%d", info, calls)
	}
	encoded, err := json.Marshal(info)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("gomobile health snapshot is not JSON: %s %v", encoded, err)
	}
}
