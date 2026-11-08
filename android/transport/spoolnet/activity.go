package spoolnet

import (
	"encoding/json"
	"net/http"
	"time"
)

func (c *Client) receivedResponse() {
	if c.ctx != nil && c.ctx.Err() != nil {
		return
	}
	now := time.Now()
	c.responseActivity.Store(&now)
}

// ResponseAgeMillis reports time since an HTTPS response or valid stream frame.
// A negative value means there is no evidence on this client's current network.
// Local writes, cached node state and another server's traffic are not evidence.
func (c *Client) ResponseAgeMillis() int64 {
	if c.ctx != nil && c.ctx.Err() != nil {
		return -1
	}
	last := c.responseActivity.Load()
	if last == nil {
		return -1
	}
	return time.Since(*last).Milliseconds()
}

func (c *Client) encodeResponse(resp *http.Response) (string, error) {
	status := resp.StatusCode
	result, err := encodeResponse(resp)
	if err == nil && status >= 200 && status < 300 {
		c.receivedResponse()
	} else if err != nil || status >= 500 {
		c.responseActivity.Store(nil)
	}
	return result, err
}

type responseActivityListener struct {
	client   *Client
	listener EventListener
}

func (l responseActivityListener) OnEvent(kind, data string) {
	if json.Valid([]byte(data)) {
		l.client.receivedResponse()
	}
	l.listener.OnEvent(kind, data)
}
