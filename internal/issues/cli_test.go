package issues

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

type cliExitError int

func (e cliExitError) Error() string { return fmt.Sprintf("exit status %d", e) }
func (e cliExitError) ExitCode() int { return int(e) }

func TestCheckCLI(t *testing.T) {
	for _, tc := range []struct {
		name       string
		versionErr error
		apiErr     error
		body       string
		want       CLIStatus
	}{
		{name: "ready", body: `{"login":"octocat"}`, want: CLIStatus{State: CLIReady, Login: "octocat"}},
		{name: "not installed", versionErr: fmt.Errorf("gh: %w", exec.ErrNotFound), want: CLIStatus{State: CLIMissing}},
		{name: "removed executable", versionErr: &os.PathError{Op: "fork/exec", Path: "gh", Err: os.ErrNotExist}, want: CLIStatus{State: CLIMissing}},
		{name: "cannot execute", versionErr: os.ErrPermission, want: CLIStatus{State: CLIUnavailable}},
		{name: "not logged in", apiErr: fmt.Errorf("gh: %w", cliExitError(4)), want: CLIStatus{State: CLILoginRequired}},
		{name: "login guidance", apiErr: errors.New("To get started with GitHub CLI, please run: gh auth login"), want: CLIStatus{State: CLILoginRequired}},
		{name: "invalid credentials", apiErr: errors.New("HTTP 401: Bad credentials (ghp-do-not-display)"), want: CLIStatus{State: CLIInvalidCredentials}},
		{name: "expired credentials", apiErr: errors.New("token has expired"), want: CLIStatus{State: CLIInvalidCredentials}},
		{name: "DNS failure", apiErr: errors.New("could not resolve host api.github.com"), want: CLIStatus{State: CLINetworkUnavailable}},
		{name: "network error", apiErr: &net.DNSError{Err: "lookup failed", Name: "api.github.com"}, want: CLIStatus{State: CLINetworkUnavailable}},
		{name: "timeout", apiErr: context.DeadlineExceeded, want: CLIStatus{State: CLINetworkUnavailable}},
		{name: "timeout with login advice", apiErr: errors.New("error connecting to api.github.com; try gh auth login"), want: CLIStatus{State: CLINetworkUnavailable}},
		{name: "permission failure", apiErr: errors.New("HTTP 403: forbidden"), want: CLIStatus{State: CLIVerificationFailed}},
		{name: "invalid JSON", body: `not JSON`, want: CLIStatus{State: CLIVerificationFailed}},
		{name: "anonymous response", body: `{}`, want: CLIStatus{State: CLIVerificationFailed}},
		{name: "empty response", body: `null`, want: CLIStatus{State: CLIVerificationFailed}},
		{name: "empty account", body: `{"login":"  "}`, want: CLIStatus{State: CLIVerificationFailed}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			status := CheckCLI(context.Background(), func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
				if directory != "" || name != "gh" {
					t.Fatalf("unexpected command: %s in %s", name, directory)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("startup check must have a bounded timeout")
				}
				calls = append(calls, args)
				if len(calls) == 1 {
					return []byte("gh version fixture"), tc.versionErr
				}
				return []byte(tc.body), tc.apiErr
			})
			if status != tc.want {
				t.Fatalf("got %#v, want %#v", status, tc.want)
			}
			wantCalls := [][]string{{"--version"}}
			if tc.versionErr == nil {
				wantCalls = append(wantCalls, []string{"api", "--hostname", "github.com", "user"})
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("got commands %v, want %v", calls, wantCalls)
			}
		})
	}
}
