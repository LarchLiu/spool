package agent

import (
	"context"
	"os/exec"
	"strings"
	"time"

	chatmodel "spool/internal/model"
)

// ConfigureBinaries applies executable overrides without depending on persistence.
func (r *CLIRunner) ConfigureBinaries(binaries map[chatmodel.Backend]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.binaryOverrides = make(map[chatmodel.Backend]string, len(binaries))
	for backend, binary := range binaries {
		r.binaryOverrides[backend] = binary
	}
}

// Version probes a CLI with the same process-group cleanup as chat processes.
func Version(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	configureAgentCommand(cmd)
	cmd.WaitDelay = agentExitGracePeriod
	buf := limitedBuffer{limit: 1024}
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
