package workspace

import "sync"

type changeKind uint8

const (
	changeNavigation changeKind = 1 << iota
	changeAgents
	changePresence
	changeAuth
	changeProfiles
)

// Notifications carry no user data. Each subscriber reads a fresh, authorized
// snapshot; coalescing retains every kind even when a client is slow.
type changeHub struct {
	mu   sync.Mutex
	subs map[*changeSubscription]struct{}
}

type changeSubscription struct {
	hub     *changeHub
	wake    chan struct{}
	pending changeKind
}

func (h *changeHub) subscribe() *changeSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = make(map[*changeSubscription]struct{})
	}
	s := &changeSubscription{hub: h, wake: make(chan struct{}, 1)}
	h.subs[s] = struct{}{}
	return s
}

func (h *changeHub) publish(kind changeKind) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		sub.pending |= kind
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *changeSubscription) take() changeKind {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	kind := s.pending
	s.pending = 0
	return kind
}

func (s *changeSubscription) close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	delete(s.hub.subs, s)
}

func (s *Store) notify(err error, kind changeKind) error {
	if err == nil {
		s.changes.publish(kind)
	}
	return err
}
