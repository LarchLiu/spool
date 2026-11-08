// Package relay provides outbound-only, end-to-end TLS tunnels for Spool.
// Each HTTP connection or WebSocket gets an independent relay data connection,
// so a slow file transfer or desktop viewer cannot block another stream.
package relay
