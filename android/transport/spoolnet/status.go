package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

var errNodeStatusUnavailable = errors.New("embedded node status is unavailable")

type nodeStatusStream interface {
	Next() (ipn.Notify, error)
	Close() error
}

type nodeSnapshot struct {
	State   string `json:"state"`
	AuthURL string `json:"authURL"`
}

// One subscription belongs to the client, including while Android is hidden.
// Readers share its small snapshot; UI subscribers never own another IPN watch.
type nodeStatus struct {
	ctx     context.Context
	done    chan struct{}
	mu      sync.Mutex
	value   nodeSnapshot
	err     error
	changed chan struct{}
}

func newNodeStatus(ctx context.Context, open func(context.Context) (nodeStatusStream, error)) *nodeStatus {
	state := &nodeStatus{ctx: ctx, done: make(chan struct{}), changed: make(chan struct{})}
	go func() {
		defer close(state.done)
		for ctx.Err() == nil {
			watcher, err := open(ctx)
			if err == nil {
				func() {
					defer watcher.Close()
					var value nodeSnapshot
					for {
						update, err := watcher.Next()
						if err != nil {
							return
						}
						if update.BrowseToURL != nil {
							value.AuthURL = ""
							if validNodeLoginURL(*update.BrowseToURL) {
								value.AuthURL = *update.BrowseToURL
							}
						}
						if update.State != nil {
							value.State = update.State.String()
							if *update.State != ipn.NeedsLogin {
								value.AuthURL = ""
							}
						}
						if update.LoginFinished != nil {
							value.AuthURL = ""
						}
						if value.State != "" {
							state.publish(value, nil)
						}
					}
				}()
			}
			if ctx.Err() != nil {
				return
			}
			state.publish(nodeSnapshot{State: "Unknown"}, errNodeStatusUnavailable)
			if waitContext(ctx, time.Second) != nil {
				return
			}
		}
	}()
	return state
}

func (s *nodeStatus) publish(value nodeSnapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil || s.value == value && s.err == err {
		return
	}
	s.value, s.err = value, err
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *nodeStatus) snapshot(ctx context.Context) (nodeSnapshot, <-chan struct{}, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nodeSnapshot{}, nil, err
		}
		if err := s.ctx.Err(); err != nil {
			return nodeSnapshot{}, nil, err
		}
		s.mu.Lock()
		value, changed, err := s.value, s.changed, s.err
		s.mu.Unlock()
		if value.State != "" || err != nil {
			return value, changed, err
		}
		select {
		case <-ctx.Done():
		case <-s.ctx.Done():
		case <-changed:
		}
	}
}

func (s *nodeStatus) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	value, _, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &ipnstate.Status{BackendState: value.State, AuthURL: value.AuthURL}, nil
}

// SubscribeStatus delivers the initial node state and subsequent changes.
// Callbacks run off the UI thread; closing the handle or client stops delivery.
func (c *Client) SubscribeStatus(listener EventListener) (*EventStream, error) {
	if listener == nil || c.direct || c.node == nil {
		return nil, errors.New("node status subscription requires a Tailscale client and listener")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	stream := &EventStream{ctx: ctx, cancel: cancel}
	go func() {
		defer cancel()
		for ctx.Err() == nil {
			value, changed, _ := c.node.snapshot(ctx)
			if ctx.Err() != nil {
				return
			}
			data, _ := json.Marshal(value)
			listener.OnEvent("status", string(data))
			select {
			case <-ctx.Done():
			case <-changed:
			}
		}
	}()
	return stream, nil
}

// LoginURL waits for the login notification instead of polling node status.
func (c *Client) LoginURL() (string, error) {
	if c.direct || c.node == nil {
		return "", errors.New("Tailscale login is unavailable for direct HTTPS connections")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	value, _, _ := c.node.snapshot(ctx)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if validNodeLoginURL(value.AuthURL) {
		return value.AuthURL, nil
	}
	if err := c.Login(); err != nil {
		return "", err
	}
	for {
		value, changed, err := c.node.snapshot(ctx)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil && validNodeLoginURL(value.AuthURL) {
			return value.AuthURL, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		}
	}
}

func validNodeLoginURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && u.Host == "login.tailscale.com" && u.User == nil && u.Fragment == ""
}
