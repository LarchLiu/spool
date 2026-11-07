package spoolnet

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

type fakeReconnectAPI struct {
	state     string
	statusErr error
	downErr   error
	upErr     error
	cancel    context.CancelFunc
	edits     []bool
}

func (f *fakeReconnectAPI) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &ipnstate.Status{BackendState: f.state}, f.statusErr
}

func (f *fakeReconnectAPI) EditPrefs(ctx context.Context, prefs *ipn.MaskedPrefs) (*ipn.Prefs, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !prefs.WantRunningSet || prefs.LoggedOutSet || prefs.ControlURLSet {
		panic("reconnect must only edit WantRunning")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		panic("reconnect must have a deadline")
	}
	f.edits = append(f.edits, prefs.WantRunning)
	if !prefs.WantRunning {
		if f.cancel != nil {
			f.cancel()
		}
		return nil, f.downErr
	}
	return nil, f.upErr
}

func TestReconnectBouncesOnlyRunningAndPreservesEnrollment(t *testing.T) {
	for _, test := range []struct {
		state string
		want  []bool
	}{
		{"Running", []bool{false, true}},
		{"Stopped", []bool{true}},
		{"Starting", []bool{true}},
		{"NeedsLogin", nil},
		{"NeedsMachineAuth", nil},
	} {
		t.Run(test.state, func(t *testing.T) {
			api := &fakeReconnectAPI{state: test.state}
			if err := reconnectNode(context.Background(), api); err != nil || !reflect.DeepEqual(api.edits, test.want) {
				t.Fatalf("edits=%v want=%v, error=%v", api.edits, test.want, err)
			}
		})
	}
}

func TestReconnectTriesToComeBackUpEvenWhenDownResponseIsLost(t *testing.T) {
	api := &fakeReconnectAPI{state: "Running", downErr: io.EOF}
	if err := reconnectNode(context.Background(), api); err != nil || !reflect.DeepEqual(api.edits, []bool{false, true}) {
		t.Fatalf("node was left down: %+v %v", api.edits, err)
	}
	api = &fakeReconnectAPI{state: "Running", upErr: io.EOF}
	if err := reconnectNode(context.Background(), api); !errors.Is(err, io.EOF) {
		t.Fatalf("lost reconnect error: %v", err)
	}
}

func TestReconnectDoesNotRestartAClosedClientOrBounceWithoutStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeReconnectAPI{state: "Running", cancel: cancel}
	if err := reconnectNode(ctx, api); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(api.edits, []bool{false}) {
		t.Fatalf("closed client came back up: edits=%v error=%v", api.edits, err)
	}
	api = &fakeReconnectAPI{statusErr: io.EOF}
	if err := reconnectNode(context.Background(), api); !errors.Is(err, io.EOF) || len(api.edits) != 0 {
		t.Fatalf("reconnected blindly: edits=%v error=%v", api.edits, err)
	}
}
