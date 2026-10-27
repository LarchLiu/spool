package appupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"
)

// NetworkError marks only failures inside an update HTTP operation. Disk,
// extraction and signature errors must never enter the network retry loop.
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "update network failure: " + SafeError(e.Err) }
func (e *NetworkError) Unwrap() error { return e.Err }

var downloadURL = regexp.MustCompile(`https?://[^\s"<>]+`)

// SafeError omits GitHub/CDN signed URLs from both the UI and local logs.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	return downloadURL.ReplaceAllString(err.Error(), "[download URL]")
}

func networkFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var op *net.OpError
	var timeout net.Error
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &op) || errors.As(err, &timeout) && timeout.Timeout() {
		return &NetworkError{err}
	}
	return err
}

func updateResponse(response *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return nil, networkFailure(err)
	}
	switch response.StatusCode {
	case 408, 429, 500, 502, 503, 504:
		response.Body.Close()
		return nil, &NetworkError{fmt.Errorf("download server returned HTTP %d", response.StatusCode)}
	}
	return response, nil
}

type networkBody struct {
	io.ReadCloser
	remaining int64
}

func (r *networkBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if err == io.EOF && r.remaining > 0 {
		err = io.ErrUnexpectedEOF
	} else if err == io.EOF {
		return n, io.EOF
	}
	return n, networkFailure(err)
}

func retryNetwork(ctx context.Context, delays []time.Duration, fn func() error, retry func(error)) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		var network *NetworkError
		if err == nil || ctx.Err() != nil || !errors.As(err, &network) || attempt >= len(delays) {
			return err
		}
		if retry != nil {
			retry(err)
		}
		timer := time.NewTimer(delays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
