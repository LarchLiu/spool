// Package issues implements the local, fail-closed admission phase of issue automation.
package issues

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Command func(context.Context, string, string, ...string) ([]byte, error)

func Run(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	if name == "gh" {
		cmd.Env = append(cmd.Env, "GH_HOST=github.com")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4096 {
			message = message[:4096]
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, message)
	}
	return out, nil
}

func RemoteError(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"could not resolve", "no such host", "dial tcp", "timeout", "timed out", "network is unreachable", "connection refused", "connection reset", "error connecting", "tls handshake"} {
		if strings.Contains(text, marker) {
			return fmt.Errorf("GitHub 网络不可达，台账未变更、未认领任务。若受沙箱限制，请按环境权限流程批准沙箱外联网后重试；不要据此重新登录：%w", err)
		}
	}
	if strings.Contains(text, "http 401") || strings.Contains(text, "bad credentials") || strings.Contains(text, "token has expired") {
		return fmt.Errorf("GitHub 明确返回凭据无效或过期，台账未变更；请检查凭据：%w", err)
	}
	return fmt.Errorf("GitHub 数据读取失败，台账未变更、未认领任务（403/404 也可能是仓库权限问题）：%w", err)
}
