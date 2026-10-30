package spoolnet

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

func parseEndpoint(address string) (*url.URL, string, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(address, "#") {
		return nil, "", errors.New("enter an HTTPS server origin, without a path, credentials, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if !dnsName.MatchString(host) || host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.Contains(host, "..") || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return nil, "", errors.New("use the server's HTTPS DNS name, for example spool.example.ts.net")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, "", errors.New("invalid HTTPS port")
	}
	u.Host = host
	if port != "443" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Path = ""
	return u, net.JoinHostPort(host, port), nil
}
