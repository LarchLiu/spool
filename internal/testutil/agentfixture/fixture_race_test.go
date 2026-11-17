//go:build race

package agentfixture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAgentHelperProcess(t *testing.T) {
	if os.Getenv("SPOOL_TEST_AGENT") != "1" {
		return
	}
	if os.Getenv("SPOOL_TEST_FIXTURE_RACE") == "1" {
		// Only the child deliberately races. Its diagnostic and configured exit
		// status must survive the fixture's shorter exit path.
		var value int
		done := make(chan struct{})
		go func() { value = 1; close(done) }()
		value = 2
		<-done
		fmt.Println(value)
		return
	}
	Run(t)
}

func TestFixtureChildStillReportsRaces(t *testing.T) {
	t.Setenv("GORACE", "halt_on_error=1 exitcode=23")
	t.Setenv("SPOOL_TEST_FIXTURE_RACE", "1")
	fixture := New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, fixture.Binary, "--version").CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 || !strings.Contains(string(output), "WARNING: DATA RACE") {
		t.Fatalf("fixture lost race detection or caller options: %v\n%s", err, output)
	}
}
