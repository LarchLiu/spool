package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spool/relay"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8788", "HTTP listen address (put a TLS reverse proxy in front)")
	adminListen := flag.String("admin-listen", "127.0.0.1:8787", "private administrative HTTP listen address")
	dataDir := flag.String("data-dir", "relay-data", "persistent registration and revocation directory")
	requireToken := flag.Bool("require-registration-token", true, "require a per-server token (false allows signed server-identity registration)")
	cert := flag.String("tls-cert", "", "optional public TLS certificate PEM")
	key := flag.String("tls-key", "", "optional public TLS key PEM")
	hosts := flag.Int("max-hosts", 100, "maximum registered Spool servers")
	connections := flag.Int("max-connections", 4096, "maximum active data connections")
	perHost := flag.Int("max-connections-per-host", 128, "maximum data connections per Spool server")
	flag.Parse()
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: os.Getenv("SPOOL_RELAY_ADMIN_TOKEN"), DataDir: *dataDir, AllowUnregisteredHosts: !*requireToken, MaxHosts: *hosts, MaxConnections: *connections, MaxConnectionsPerHost: *perHost})
	if err != nil {
		log.Fatal(err)
	}
	if (*cert == "") != (*key == "") {
		log.Fatal("both tls-cert and tls-key are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: *listen, Handler: broker, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	admin := &http.Server{Addr: *adminListen, Handler: broker.AdminHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	adminListener, err := net.Listen("tcp", *adminListen)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := admin.Serve(adminListener); err != nil && err != http.ErrServerClosed {
			log.Printf("administrative listener failed: %v", err)
			cancel()
		}
	}()
	go func() { <-ctx.Done(); broker.Close(); server.Close(); admin.Close() }()
	defer broker.Close()
	defer admin.Close()
	log.Printf("Spool relay administration listening on %s", adminListener.Addr())
	log.Printf("Spool relay listening on %s", *listen)
	if *cert != "" {
		err = server.ListenAndServeTLS(*cert, *key)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
