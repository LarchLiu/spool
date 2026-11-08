package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"syscall"
	"time"
)

// Probe retries only transient failures of the unauthenticated health GET.
// API mutations are never replayed, and TLS/origin verification stays strict.
func (c *Client) Probe() error {
	ctx, cancel := context.WithTimeout(c.ctx, time.Minute)
	defer cancel()
	return retryProbe(ctx, c.probeOnce, waitContext)
}

func retryProbe(ctx context.Context, probe func(context.Context) error, wait func(context.Context, time.Duration) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := probe(requestCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 2 || !transientProbeError(err) {
			return err
		}
		if err := wait(ctx, time.Second<<attempt); err != nil {
			return err
		}
	}
}

type healthStatusError struct{ status int }

func (e healthStatusError) Error() string {
	return fmt.Sprintf("endpoint is not a healthy Spool server (HTTP %d)", e.status)
}

func transientProbeError(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	if errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalidCertificate) {
		return false
	}
	var status healthStatusError
	if errors.As(err, &status) {
		return status.status == 502 || status.status == 503 || status.status == 504
	}
	for _, transient := range []error{errServerNotReady, io.EOF, io.ErrUnexpectedEOF, context.DeadlineExceeded,
		syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED, syscall.EPIPE,
		syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.ETIMEDOUT} {
		if errors.Is(err, transient) {
			return true
		}
	}
	var network net.Error
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	return errors.As(err, &network) && network.Timeout()
}

func (c *Client) probeOnce(ctx context.Context) (runErr error) {
	defer func() {
		if runErr != nil {
			c.responseActivity.Store(nil)
		}
	}()
	var phase atomic.Value
	if c.direct {
		phase.Store("dialing HTTPS server")
	} else {
		phase.Store("dialing tailnet server")
	}
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { phase.Store("TLS handshake") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				phase.Store("sending HTTPS request")
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				phase.Store("waiting for HTTPS response")
			}
		},
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, c.origin.String()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Spool HTTPS health check failed (%s): %w", phase.Load(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthStatusError{resp.StatusCode}
	}
	var health struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health); err != nil {
		return fmt.Errorf("Spool HTTPS health check failed (reading health response): %w", err)
	}
	if !health.OK {
		return healthStatusError{resp.StatusCode}
	}
	c.receivedResponse()
	return nil
}
