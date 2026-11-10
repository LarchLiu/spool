package issues

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"spool/internal/netproxy"
)

func TestCommandsUseLiveSpoolProxyWithoutChangingParentEnvironment(t *testing.T) {
	restore, err := netproxy.Prepare(t.Context(), netproxy.Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netproxy.Activate(restore) })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOL_TEST_PROXY_COMMAND", "1")
	t.Setenv("HTTPS_PROXY", "http://stale.invalid:1")
	t.Setenv("https_proxy", "http://stale.invalid:2")
	t.Setenv("ALL_PROXY", "socks5://stale.invalid:3")
	t.Setenv("NO_PROXY", "*")
	run := WithGitHubCLI(executable)
	for _, reply := range []string{"first proxy", "second proxy"} {
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.String() != "http://github-proxy.invalid/test" {
				t.Errorf("unexpected target %q", r.URL)
			}
			_, _ = io.WriteString(w, reply)
		}))
		t.Cleanup(proxy.Close)
		policy, err := netproxy.Prepare(t.Context(), netproxy.Options{Enabled: true, ProxyURL: proxy.URL, NoProxy: "internal.example"})
		if err != nil {
			t.Fatal(err)
		}
		netproxy.Activate(policy)
		out, err := run(t.Context(), "", "gh", "-test.run=^TestProxyCommandProcess$", "--", "http://github-proxy.invalid/test")
		if err != nil || string(out) != reply {
			t.Fatal("gh child did not use the current application proxy", string(out), err)
		}
	}
	policy, err := netproxy.Prepare(t.Context(), netproxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	netproxy.Activate(policy)
	out, err := run(t.Context(), "", "gh", "-test.run=^TestProxyCommandProcess$", "--", "env")
	if err != nil || strings.TrimSpace(string(out)) != `{"HTTP_PROXY":"","HTTPS_PROXY":"","ALL_PROXY":"","NO_PROXY":"","GH_HOST":"github.com"}` {
		t.Fatal("direct child retained inherited proxy variables", string(out), err)
	}
	if os.Getenv("HTTPS_PROXY") != "http://stale.invalid:1" || os.Getenv("NO_PROXY") != "*" {
		t.Fatal("command changed the parent environment")
	}
	unavailable := errors.New("saved proxy unavailable")
	netproxy.Reject(unavailable)
	if _, err := run(t.Context(), "", "gh", "-test.run=^TestProxyCommandProcess$", "--", "env"); !errors.Is(err, unavailable) {
		t.Fatal("gh bypassed the unavailable policy", err)
	}
}

func TestProxyCommandProcess(t *testing.T) {
	if os.Getenv("SPOOL_TEST_PROXY_COMMAND") != "1" {
		return
	}
	target := os.Args[len(os.Args)-1]
	if target == "env" {
		// Report only fixture fields, never credentials from the parent env.
		fmt.Printf(`{"HTTP_PROXY":%q,"HTTPS_PROXY":%q,"ALL_PROXY":%q,"NO_PROXY":%q,"GH_HOST":%q}`,
			os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"), os.Getenv("ALL_PROXY"), os.Getenv("NO_PROXY"), os.Getenv("GH_HOST"))
		os.Exit(0)
	}
	response, err := http.Get(target)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
	_, err = io.Copy(os.Stdout, response.Body)
	response.Body.Close()
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
