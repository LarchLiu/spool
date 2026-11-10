package netproxy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentUsesActivePolicyAndPreservesOtherVariables(t *testing.T) {
	previous := activeProxy.Load()
	t.Cleanup(func() { Activate(previous) })
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", "http://environment:8080")
	t.Setenv("NO_PROXY", "inherited.example")
	base := []string{"PATH=/tools", "GH_TOKEN=fixture-token", "Http_Proxy=http://stale:1", "https_proxy=http://stale:2", "ALL_PROXY=socks5://stale:3", "No_Proxy=*"}
	saved := append([]string(nil), base...)
	for _, tc := range []struct {
		name        string
		options     Options
		http, https string
		bypass      string
	}{
		{"custom", Options{Enabled: true, ProxyURL: "http://custom:8889", NoProxy: "internal.example"}, "http://custom:8889", "http://custom:8889", "internal.example"},
		{"disabled", Options{}, "", "", ""},
		{"environment", Options{Enabled: true}, "", "http://environment:8080", "inherited.example"},
		{"system fallback", Options{Enabled: true, UseSystemProxy: true}, "http://system:8889", "http://environment:8080", "*.corp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := prepare(t.Context(), tc.options, func(context.Context) (systemProxy, error) {
				return systemProxy{http: "http://system:8889", https: "http://secure:8889", exceptions: []string{"*.corp"}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			Activate(policy)
			env, err := Environment(base)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, entry := range env {
				key, value, _ := strings.Cut(entry, "=")
				if _, duplicate := values[key]; duplicate {
					t.Fatal("duplicate environment variable", key)
				}
				values[key] = value
			}
			for key, want := range map[string]string{"HTTP_PROXY": tc.http, "http_proxy": tc.http, "HTTPS_PROXY": tc.https, "https_proxy": tc.https, "PATH": "/tools", "GH_TOKEN": "fixture-token"} {
				if values[key] != want {
					t.Fatalf("%s = %q, want %q", key, values[key], want)
				}
			}
			all := ""
			if tc.http == tc.https {
				all = tc.http
			}
			if values["ALL_PROXY"] != all || values["all_proxy"] != all {
				t.Fatal("stale or mismatched all-protocol proxy", values)
			}
			if tc.bypass != "" && (!strings.Contains(values["NO_PROXY"], tc.bypass) || !strings.Contains(values["NO_PROXY"], "127.0.0.1")) {
				t.Fatal("child lost bypass rules", values["NO_PROXY"])
			}
			if tc.http == "" && tc.https == "" {
				if values["NO_PROXY"] != "" || values["no_proxy"] != "" {
					t.Fatal("direct mode retained proxy variables")
				}
			} else if values["NODE_USE_ENV_PROXY"] != "1" {
				t.Fatal("Node fetch was not opted into proxy environment")
			}
			if !reflect.DeepEqual(base, saved) || os.Getenv("HTTPS_PROXY") != "http://environment:8080" {
				t.Fatal("child environment changed the caller or process environment")
			}
		})
	}
}

func TestEnvironmentRejectsUnavailablePolicyAndRecovers(t *testing.T) {
	previous := activeProxy.Load()
	t.Cleanup(func() { Activate(previous) })
	unavailable := errors.New("system proxy unavailable")
	Reject(unavailable)
	if env, err := Environment([]string{"HTTP_PROXY=http://stale:1"}); env != nil || !errors.Is(err, unavailable) {
		t.Fatal("child fell back to another proxy", env, err)
	}
	policy, err := Prepare(t.Context(), Options{Enabled: true, ProxyURL: "http://custom:8889"})
	if err != nil {
		t.Fatal(err)
	}
	Activate(policy)
	env, err := Environment([]string{"NODE_USE_ENV_PROXY=0"})
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "HTTP_PROXY=http://custom:8889") || !strings.Contains(strings.Join(env, "\n"), "NODE_USE_ENV_PROXY=0") {
		t.Fatal("child policy did not recover or replaced an explicit Node choice", env, err)
	}
}
