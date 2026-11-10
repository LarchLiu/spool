package workspace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// An ingress owns its HTTP connections, while the App owns ongoing agent work.
// BaseContext cancellation also reaches upgraded connections on this entry.
type httpIngress struct {
	server *http.Server
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *Service) newIngressLocked() *httpIngress {
	ctx, cancel := context.WithCancel(s.ctx)
	return &httpIngress{server: &http.Server{
		Handler: s.app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}, ctx: ctx, cancel: cancel}
}

func (i *httpIngress) close() {
	if i != nil {
		i.cancel()
		_ = i.server.Close()
	}
}

func (s *Service) startWebLocked(settings Settings) error {
	var listener net.Listener
	if settings.WebEnabled {
		var err error
		listener, err = net.Listen("tcp", settings.Address())
		if err != nil {
			return err
		}
		s.web = s.newIngressLocked()
		s.web.server.Handler = ordinaryWebHandler(s.app)
		s.runningAddress = listener.Addr().String()
	} else {
		s.runningAddress = ""
	}
	s.app.configMu.Lock()
	s.app.settings.WebEnabled = settings.WebEnabled
	s.app.settings.Host, s.app.settings.Port, s.app.settings.PublicURL = settings.Host, settings.Port, settings.PublicURL
	s.app.settings.WebTrustedProxies = settings.WebTrustedProxies
	s.app.settings.WebDetectedProxy, s.app.settings.WebDetectedOrigin = settings.WebDetectedProxy, settings.WebDetectedOrigin
	s.app.configMu.Unlock()
	if listener != nil {
		s.serveLocked(s.web.server, listener)
	}
	return nil
}

func (s *Service) restartWebLocked(settings Settings) error {
	previous := s.activeSettings
	s.web.close()
	s.web = nil
	if err := s.startWebLocked(settings); err != nil {
		// Keep the original entry usable after a bind failure; leave changes pending.
		if rollback := s.startWebLocked(previous); rollback != nil {
			s.runningAddress = ""
			s.webError = fmt.Sprintf("%v; restore previous listener: %v", err, rollback)
		} else {
			s.webError = err.Error()
		}
		return err
	}
	s.webError = ""
	return nil
}
