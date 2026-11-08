package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
)

type nodeWatchResult struct {
	update ipn.Notify
	err    error
}

type testNodeWatch struct {
	ctx     context.Context
	updates chan nodeWatchResult
	closed  atomic.Int32
}

func (w *testNodeWatch) Next() (ipn.Notify, error) {
	select {
	case <-w.ctx.Done():
		return ipn.Notify{}, w.ctx.Err()
	case result := <-w.updates:
		return result.update, result.err
	}
}

func (w *testNodeWatch) Close() error { w.closed.Add(1); return nil }

type nodeEventFunc func(string, string)

func (f nodeEventFunc) OnEvent(kind, data string) { f(kind, data) }

type nodeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nodeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNodeStatusSharesOneSubscriptionWithoutIdlePolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		watch := &testNodeWatch{updates: make(chan nodeWatchResult, 8)}
		var opens atomic.Int32
		state := newNodeStatus(ctx, func(ctx context.Context) (nodeStatusStream, error) {
			opens.Add(1)
			watch.ctx = ctx
			return watch, nil
		})
		defer func() { cancel(); <-state.done }()
		client := &Client{ctx: ctx, node: state}
		updates := make(chan nodeSnapshot, 16)
		subscribe := func() *EventStream {
			stream, err := client.SubscribeStatus(nodeEventFunc(func(kind, data string) {
				var value nodeSnapshot
				if kind != "status" || json.Unmarshal([]byte(data), &value) != nil {
					t.Error("invalid node event")
				}
				updates <- value
			}))
			if err != nil {
				t.Fatal(err)
			}
			return stream
		}
		first, second := subscribe(), subscribe()
		defer first.Close()
		defer second.Close()
		running := ipn.Running
		watch.updates <- nodeWatchResult{update: ipn.Notify{State: &running}}
		synctest.Wait()
		if len(updates) != 2 {
			t.Fatal("subscribers did not receive the initial state")
		}
		<-updates
		<-updates
		for range 50 {
			if _, err := client.Status(); err != nil {
				t.Fatal(err)
			}
			if health := checkHealth(ctx, state, func(context.Context) error { return nil }); health.HTTPS != "Verified" {
				t.Fatal("health did not use the subscribed state", health)
			}
		}
		watch.updates <- nodeWatchResult{update: ipn.Notify{State: &running}}
		watch.updates <- nodeWatchResult{update: ipn.Notify{Version: "unchanged"}}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if opens.Load() != 1 || len(updates) != 0 {
			t.Fatal("idle state was polled or repeatedly emitted")
		}
		first.Close()
		needsLogin, address := ipn.NeedsLogin, "https://login.tailscale.com/a/test"
		watch.updates <- nodeWatchResult{update: ipn.Notify{State: &needsLogin, BrowseToURL: &address}}
		synctest.Wait()
		if len(updates) != 1 {
			t.Fatal("closed subscriber received a later update")
		}
		if value := <-updates; value.State != "NeedsLogin" || value.AuthURL != address {
			t.Fatal(value)
		}
		watch.updates <- nodeWatchResult{update: ipn.Notify{State: &running}}
		synctest.Wait()
		if value := <-updates; value.State != "Running" || value.AuthURL != "" {
			t.Fatal("stale login URL", value)
		}
		cancel()
		<-state.done
		synctest.Wait()
		if watch.closed.Load() != 1 || len(updates) != 0 {
			t.Fatal("client cancellation leaked state callbacks")
		}
	})
}

func TestNodeStatusReconnectInvalidatesStaleState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var opens atomic.Int32
		var watches []*testNodeWatch
		state := newNodeStatus(ctx, func(ctx context.Context) (nodeStatusStream, error) {
			attempt := opens.Add(1)
			if attempt == 1 {
				return nil, io.EOF
			}
			watch := &testNodeWatch{ctx: ctx, updates: make(chan nodeWatchResult, 2)}
			watches = append(watches, watch)
			running := ipn.Running
			watch.updates <- nodeWatchResult{update: ipn.Notify{State: &running}}
			return watch, nil
		})
		defer func() { cancel(); <-state.done }()
		synctest.Wait()
		if _, _, err := state.snapshot(ctx); !errors.Is(err, errNodeStatusUnavailable) {
			t.Fatal("missing subscription error", err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if value, _, err := state.snapshot(ctx); err != nil || value.State != "Running" {
			t.Fatal("subscription did not recover", value, err)
		}
		watches[0].updates <- nodeWatchResult{err: io.ErrUnexpectedEOF}
		synctest.Wait()
		if value, _, err := state.snapshot(ctx); !errors.Is(err, errNodeStatusUnavailable) || value.State != "Unknown" {
			t.Fatal("dropped stream retained a stale Running state", value, err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if opens.Load() != 3 {
			t.Fatal("stream was not reopened with backoff")
		}
		cancel()
		<-state.done
		for _, watch := range watches {
			if watch.closed.Load() != 1 {
				t.Fatal("subscription was not closed")
			}
		}
	})
}

func TestNodeLoginURLWaitsForNotificationAndBoundsCancellation(t *testing.T) {
	for _, mode := range []string{"event", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				watch := &testNodeWatch{updates: make(chan nodeWatchResult, 2)}
				state := newNodeStatus(ctx, func(ctx context.Context) (nodeStatusStream, error) {
					watch.ctx = ctx
					return watch, nil
				})
				defer func() { cancel(); <-state.done }()
				needsLogin := ipn.NeedsLogin
				watch.updates <- nodeWatchResult{update: ipn.Notify{State: &needsLogin}}
				var requests atomic.Int32
				client := &Client{ctx: ctx, node: state, local: &local.Client{OmitAuth: true, Transport: nodeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					requests.Add(1)
					if !strings.HasSuffix(r.URL.Path, "/login-interactive") {
						t.Errorf("unexpected local request %s", r.URL.Path)
					}
					go func() {
						if waitContext(ctx, 2*time.Second) != nil {
							return
						}
						if mode == "event" {
							address := "https://login.tailscale.com/a/test"
							watch.updates <- nodeWatchResult{update: ipn.Notify{BrowseToURL: &address}}
						}
						if mode == "close" {
							cancel()
						}
					}()
					return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
				})}}
				address, err := client.LoginURL()
				if mode == "event" {
					if err != nil || !validNodeLoginURL(address) {
						t.Fatal("notification login URL was lost", address, err)
					}
					if again, err := client.LoginURL(); err != nil || again != address || requests.Load() != 1 {
						t.Fatal("existing login URL was regenerated")
					}
				} else {
					want := context.DeadlineExceeded
					if mode == "close" {
						want = context.Canceled
					}
					if address != "" || !errors.Is(err, want) {
						t.Fatal("login wait did not stop", address, err)
					}
				}
			})
		})
	}
}
