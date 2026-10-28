package issues

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CLIState string

const (
	CLIReady              CLIState = "ready"
	CLIMissing            CLIState = "missing"
	CLIUnavailable        CLIState = "unavailable"
	CLILoginRequired      CLIState = "login_required"
	CLIInvalidCredentials CLIState = "invalid_credentials"
	CLINetworkUnavailable CLIState = "network_unavailable"
	CLIVerificationFailed CLIState = "verification_failed"
)

type CLIStatus struct {
	State CLIState
	Login string
}

// CheckCLI uses the same environment as issue automation, without exposing
// credential-bearing command output to the desktop or persisting it.
func CheckCLI(ctx context.Context, run Command) CLIStatus {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := run(ctx, "", "gh", "--version"); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return CLIStatus{State: CLIMissing}
		}
		return CLIStatus{State: CLIUnavailable}
	}
	// /user requires authentication: a local token alone does not prove that
	// the active account can make successful GitHub requests.
	body, err := run(ctx, "", "gh", "api", "--hostname", "github.com", "user")
	if err != nil {
		if networkError(err) {
			return CLIStatus{State: CLINetworkUnavailable}
		}
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "http 401") || strings.Contains(text, "bad credentials") || strings.Contains(text, "token has expired") {
			return CLIStatus{State: CLIInvalidCredentials}
		}
		var exit interface{ ExitCode() int }
		if (errors.As(err, &exit) && exit.ExitCode() == 4) || strings.Contains(text, "gh auth login") {
			return CLIStatus{State: CLILoginRequired}
		}
		return CLIStatus{State: CLIVerificationFailed}
	}
	var user struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(body, &user) != nil || strings.TrimSpace(user.Login) == "" {
		return CLIStatus{State: CLIVerificationFailed}
	}
	return CLIStatus{State: CLIReady, Login: user.Login}
}
