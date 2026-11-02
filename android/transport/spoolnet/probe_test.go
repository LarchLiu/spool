package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProbeRetriesOnlyBoundedTransientFailures(t *testing.T) {
	for _, want := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.ECONNREFUSED,
		context.DeadlineExceeded, errServerNotReady, healthStatusError{503}} {
		t.Run(want.Error(), func(t *testing.T) {
			var attempts int
			var delays []time.Duration
			err := retryProbe(context.Background(), func(ctx context.Context) error {
				attempts++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 20*time.Second {
					t.Error("probe attempt has no bounded deadline")
				}
				return fmt.Errorf("wrapped network failure: %w", want)
			}, func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil })
			if !errors.Is(err, want) || attempts != 3 || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second}) {
				t.Fatalf("incorrect bounded retries: attempts=%d delays=%v error=%v", attempts, delays, err)
			}
		})
	}
}

func TestProbeNeverRetriesCertificateOriginOrInvalidHealthFailures(t *testing.T) {
	for _, want := range []error{
		&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		x509.UnknownAuthorityError{}, x509.HostnameError{Certificate: &x509.Certificate{}, Host: "wrong.example"},
		x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired},
		tls.RecordHeaderError{Msg: "invalid TLS record"},
		errors.New("redirects are not allowed"), errors.New("wrong origin"),
		healthStatusError{200}, healthStatusError{401}, healthStatusError{403}, healthStatusError{404},
		&url.Error{Op: "Get", URL: "https://example.com/healthz", Err: x509.UnknownAuthorityError{}},
	} {
		t.Run(fmt.Sprintf("%T/%s", want, want), func(t *testing.T) {
			var attempts int
			err := retryProbe(context.Background(), func(context.Context) error { attempts++; return want },
				func(context.Context, time.Duration) error { t.Fatal("retried a permanent/security error"); return nil })
			if err != want || attempts != 1 {
				t.Fatalf("permanent error was retried: %d, %v", attempts, err)
			}
		})
	}
}

func TestProbeSucceedsAfterTransientEOFWithoutRecreatingClient(t *testing.T) {
	var attempts int
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/healthz" || r.Method != "GET" {
			t.Error("retried an authenticated API or mutation instead of health GET")
		}
		if attempts == 1 {
			return
		}
		io.WriteString(w, `{"ok":true}`)
	})
	if err := retryProbe(client.ctx, client.probeOnce, func(context.Context, time.Duration) error { return nil }); err != nil || attempts != 2 {
		t.Fatalf("did not recover from empty/EOF response: attempts=%d error=%v", attempts, err)
	}
}

func TestProbeRetriesPrematureTLSCloseAndReportsTheFailingPhase(t *testing.T) {
	var requests, attempts, dials int
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, `{"ok":true}`)
	})
	dial := client.transport.DialContext
	client.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		if dials == 1 {
			conn, remote := net.Pipe()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer remote.Close()
				_, _ = remote.Read(make([]byte, 64<<10))
			}()
			t.Cleanup(func() { conn.Close(); remote.Close(); <-done })
			return conn, nil
		}
		return dial(ctx, network, address)
	}
	err := retryProbe(client.ctx, func(ctx context.Context) error {
		attempts++
		err := client.probeOnce(ctx)
		if attempts == 1 && (err == nil || !strings.Contains(err.Error(), "TLS handshake")) {
			t.Errorf("premature TLS close lost request phase: %v", err)
		}
		return err
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil || attempts != 2 || dials != 2 || requests != 1 {
		t.Fatalf("failed to recover from premature TLS close: attempts=%d dials=%d requests=%d error=%v", attempts, dials, requests, err)
	}
}

func TestProbeOverallDeadlineStopsRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var attempts int
	err := retryProbe(ctx, func(ctx context.Context) error {
		attempts++
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context, time.Duration) error {
		t.Fatal("overall deadline scheduled another attempt")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("overall deadline failed to bound attempts: %d, %v", attempts, err)
	}
}

func TestProbeCancellationStopsAttemptsAndBackoff(t *testing.T) {
	for _, when := range []string{"before start", "during request", "during backoff"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var attempts, waits int
			if when == "before start" {
				cancel()
			}
			err := retryProbe(ctx, func(context.Context) error {
				attempts++
				if when == "during request" {
					cancel()
				}
				return io.EOF
			}, func(ctx context.Context, _ time.Duration) error {
				waits++
				cancel()
				return waitContext(ctx, time.Hour)
			})
			if !errors.Is(err, context.Canceled) || attempts > 1 || waits > 1 || when == "before start" && attempts != 0 || when != "during backoff" && waits != 0 {
				t.Fatalf("cancellation did not stop work: attempts=%d waits=%d error=%v", attempts, waits, err)
			}
		})
	}
}

func TestProbeRejectsRedirectsBadStatusAndInvalidHealth(t *testing.T) {
	for _, name := range []string{"redirect", "forbidden", "not Spool", "not healthy", "untrusted certificate"} {
		t.Run(name, func(t *testing.T) {
			var requests int
			client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch name {
				case "redirect":
					http.Redirect(w, r, "https://attacker.example/healthz", http.StatusFound)
				case "forbidden":
					w.WriteHeader(http.StatusForbidden)
				case "not Spool":
					io.WriteString(w, `<html>login required</html>`)
				case "not healthy":
					io.WriteString(w, `{"ok":false}`)
				default:
					io.WriteString(w, `{"ok":true}`)
				}
			})
			if name == "untrusted certificate" {
				client.transport.TLSClientConfig.RootCAs = x509.NewCertPool()
			}
			err := client.Probe()
			if err == nil || requests > 1 || transientProbeError(err) {
				t.Fatalf("permanent HTTPS error was accepted/retried: requests=%d error=%v", requests, err)
			}
			if name == "untrusted certificate" && !strings.Contains(err.Error(), "TLS handshake") {
				t.Fatalf("TLS failure omitted its phase: %v", err)
			}
		})
	}
}
