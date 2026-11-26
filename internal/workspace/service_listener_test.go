package workspace

import (
	"errors"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// reserveServicePort keeps the socket bound until the service takes ownership.
// Later restarts and unreserved addresses still use real net.Listen calls.
func reserveServicePort(t *testing.T, service *Service, host string) int {
	t.Helper()
	return reserveServiceListener(t, service, host).Addr().(*net.TCPAddr).Port
}

func reserveServiceListener(t *testing.T, service *Service, host string) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	// Both this cleanup and listener handoff serialize on the service lock.
	t.Cleanup(func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	address := net.JoinHostPort(host, strconv.Itoa(port))
	service.mu.Lock()
	defer service.mu.Unlock()
	previous := service.openTCPListener
	if previous == nil {
		previous = net.Listen
	}
	service.openTCPListener = func(network, requested string) (net.Listener, error) {
		if network == "tcp" && requested == address && listener != nil {
			reserved := listener
			listener = nil
			return reserved, nil
		}
		return previous(network, requested)
	}
	return listener
}

func reserveRelayPort(t *testing.T, service *Service) int {
	t.Helper()
	return reserveServicePort(t, service, "")
}

func TestReservedServiceListenersStayBoundUntilStartup(t *testing.T) {
	for _, entry := range []string{"web", "relay"} {
		t.Run(entry, func(t *testing.T) {
			service, settings := settingsService(t)
			host := settings.Host
			if entry == "relay" {
				host = ""
			}
			reserved := reserveServiceListener(t, service, host)
			port := reserved.Addr().(*net.TCPAddr).Port
			address := net.JoinHostPort(host, strconv.Itoa(port))
			requireReserved := func() {
				t.Helper()
				if listener, err := net.Listen("tcp", address); err == nil {
					_ = listener.Close()
					t.Fatal("another listener took the reserved port")
				}
			}
			requireReserved()
			if entry == "relay" {
				t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
				broker, token := testRelayBroker(t)
				public := httptest.NewServer(broker)
				t.Cleanup(public.Close)
				settings.RelayURL, settings.RelayLANPort = public.URL, port
				if err := service.SaveSettings(settings); err != nil {
					t.Fatal(err)
				}
				if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
					t.Fatal(err)
				}
				requireReserved() // Registration must never release the LAN socket.
				settings.RelayEnabled = true
			} else {
				settings.Port = port
			}
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.ApplyServiceChanges(entry); err != nil {
				t.Fatal("service failed to take ownership of reserved listener", err)
			}
			if entry == "relay" {
				waitTailnet(t, service, func(status ServiceStatus) bool { return status.Relay.State == "connected" })
			} else {
				requireServiceHealth(t, "http://"+address)
			}
			requireReserved()
			if err := service.Stop(); err != nil {
				t.Fatal(err)
			}
			if err := reserved.(*net.TCPListener).SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
				t.Fatal("service stop did not close its listener", err)
			}
		})
	}
}

func TestUnusedServicePortReservationIsCleanedUp(t *testing.T) {
	var reserved net.Listener
	t.Run("unused", func(t *testing.T) {
		service, settings := settingsService(t)
		reserved = reserveServiceListener(t, service, settings.Host)
	})
	if reserved == nil {
		t.Fatal("listener reservation failed")
	}
	if err := reserved.(*net.TCPListener).SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatal("unused reservation was not closed by test cleanup", err)
	}
}
