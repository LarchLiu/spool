//go:build !android && !ios

package spoolnet

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// HTTPSOrigin applies the same origin policy as the mobile native transports.
func HTTPSOrigin(address string) (string, error) {
	origin, _, err := parseEndpoint(address)
	if err != nil {
		return "", err
	}
	return origin.String(), nil
}

// RoundTrip exposes the same pinned network path to the desktop's loopback
// gateway. The gateway owns browser cookies; the native session jar is unused.
// Keep this Go-only API out of the mobile bindings.
func (c *Client) RoundTrip(request *http.Request) (*http.Response, error) {
	c.mu.Lock()
	closed, origin, transport, lifetime := c.closed, c.origin, c.transport, c.ctx
	c.mu.Unlock()
	if closed || origin == nil || request.URL.Scheme != origin.Scheme || request.URL.Host != origin.Host || request.URL.User != nil {
		return nil, errors.New("request must use this connection's exact server origin")
	}
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(lifetime, cancel)
	response, err := transport.RoundTrip(request.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	// A streamed response owns its cancellation until its body is closed.
	response.Body = &desktopResponseBody{ReadCloser: response.Body, close: func() { stop(); cancel() }}
	return response, nil
}

type desktopResponseBody struct {
	io.ReadCloser
	close func()
}

func (b *desktopResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.close()
	return err
}

func (b *desktopResponseBody) Write(data []byte) (int, error) {
	if writer, ok := b.ReadCloser.(io.Writer); ok {
		return writer.Write(data)
	}
	return 0, errors.New("response is not an upgraded connection")
}
