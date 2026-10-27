package appupdate

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRetriesOnlyTransientUpdateNetworkErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		tries int
	}{
		{"disconnect", networkFailure(io.ErrUnexpectedEOF), 3},
		{"connection", networkFailure(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}), 3},
		{"signature", errors.New("invalid signature"), 1},
		{"disk", errors.New("disk full"), 1},
		{"canceled", context.Canceled, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tries := 0
			err := retryNetwork(context.Background(), []time.Duration{0, 0}, func() error { tries++; return tc.err }, nil)
			if !errors.Is(err, tc.err) || tries != tc.tries {
				t.Fatalf("%d tries, %v", tries, err)
			}
		})
	}
	for _, code := range []int{200, 401, 403, 404, 408, 429, 500, 502, 503, 504} {
		res := &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}
		_, err := updateResponse(res, nil)
		if retry := code == 408 || code == 429 || code >= 500; retry != (err != nil) {
			t.Fatalf("HTTP %d classified incorrectly: %v", code, err)
		}
	}
}

func TestTruncatedArchivesAreNetworkFailuresButCompleteEOFIsNot(t *testing.T) {
	for _, size := range []int64{3, 10} {
		body := &networkBody{ReadCloser: io.NopCloser(strings.NewReader("app")), remaining: size}
		_, err := io.ReadAll(body)
		if (size == 10) != (err != nil) {
			t.Fatalf("size %d: %v", size, err)
		}
		if size == 10 {
			var network *NetworkError
			if !errors.As(err, &network) {
				t.Fatal(err)
			}
		}
	}
}

func TestRetryCancellationAndURLRedaction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := retryNetwork(ctx, []time.Duration{time.Hour}, func() error { cancel(); return &NetworkError{io.EOF} }, nil)
	if err == nil {
		t.Fatal("cancellation reported success")
	}
	err = &url.Error{Op: "Get", URL: "https://cdn.test/app?token=secret", Err: io.ErrUnexpectedEOF}
	if strings.Contains(SafeError(err), "secret") || !strings.Contains(SafeError(err), "unexpected EOF") {
		t.Fatal(SafeError(err))
	}
}

func TestControllerRetriesDownloadWithoutRepeatingSecurityFailures(t *testing.T) {
	for _, secure := range []bool{false, true} {
		installs, checks, retries := 0, 0, 0
		c := New(Options{
			Enabled: true, Preferences: func() (Preferences, error) { return Preferences{true, true}, nil },
			Check: func(context.Context) (*Release, error) {
				checks++
				return &Release{Version: "1.2.3", Install: func(context.Context, func(int64, int64)) error {
					installs++
					if secure {
						return errors.New("invalid archive signature")
					}
					if installs < 3 {
						return &NetworkError{io.ErrUnexpectedEOF}
					}
					return nil
				}}, nil
			},
			Acquire: func() (func(), error) { return func() {}, nil },
			Log: func(phase, version string, err error) {
				if phase == "install_retry" {
					retries++
				}
			},
		})
		c.retryDelays = []time.Duration{0, 0}
		next := c.poll(time.Time{})
		if secure {
			c.poll(next)
			c.Check() // Finding the same version does not clear its security hold.
			c.poll(time.Now().Add(time.Hour))
			if installs != 1 || retries != 0 || c.State().Installed {
				t.Fatalf("security failure was retried: %d", installs)
			}
			c.Install() // An explicit user request can retry after fixing the cause.
			if installs != 2 {
				t.Fatal("manual retry was blocked")
			}
		} else if installs != 3 || retries != 2 || !c.State().Installed || checks != 1 {
			t.Fatalf("retry failed: %+v %d %d", c.State(), installs, retries)
		}
		c.Close()
	}
}
