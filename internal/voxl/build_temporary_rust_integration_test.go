//go:build linux

package voxl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in on a disposable Linux host. This downloads real Rust and builds a
// dependency-free crate; ordinary unit tests never need network or host Rust.
func TestTemporaryRustDownloadBuildAndCleanup(t *testing.T) {
	if os.Getenv("SPOOL_TEST_TEMPORARY_RUST") != "1" {
		t.Skip("set SPOOL_TEST_TEMPORARY_RUST=1 for real Rust downloads")
	}
	for _, scenario := range []string{"success", "failure", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "system-bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// The real host's Rust and JavaScript tools remain untouched and are
			// absent from the environment seen by this installation job.
			for _, name := range []string{"cc", "as", "ld"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			tmp := filepath.Join(root, "tmp")
			home := filepath.Join(root, "home")
			for _, dir := range []string{tmp, home} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			env := withoutEnv(os.Environ(), "HOME", "PATH", "TMPDIR", "TMP", "TEMP", "CARGO_HOME", "RUSTUP_HOME", "RUSTUP_TOOLCHAIN")
			env = append(env, "HOME="+home, "PATH="+bin, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
			b := newBuildEnvironment(env, os.Stdout, func(step string) { t.Log(step) })
			b.set("PATH", bin)
			for _, name := range []string{"bun", "node", "cargo", "rustc", "rustup"} {
				if paths := b.find(name); len(paths) != 0 {
					t.Fatalf("host tool leaked into isolated job: %s %v", name, paths)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
			defer cancel()
			var installed string
			prepared := false
			err := func() error {
				defer b.cleanupTemporaryRust()
				cargo, err := b.prepareTemporaryRust(ctx)
				if err != nil {
					return err
				}
				prepared = true
				t.Logf("real temporary Cargo: %s", cargo)
				crate := filepath.Join(root, "crate")
				if err := os.MkdirAll(filepath.Join(crate, "src"), 0700); err != nil {
					return err
				}
				files := map[string]string{
					"Cargo.toml":  "[package]\nname = \"spool-cleanup-check\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
					"src/main.rs": "fn main() { println!(\"temporary-rust-build-ok\"); }\n",
				}
				if scenario == "failure" {
					files["src/main.rs"] = "intentional compiler error\n"
				}
				marker := filepath.Join(root, "build-started")
				if scenario == "canceled" {
					files["build.rs"] = fmt.Sprintf("fn main() { std::fs::write(%q, b\"started\").unwrap(); std::thread::sleep(std::time::Duration::from_secs(120)); }\n", marker)
				}
				for name, data := range files {
					if err := os.WriteFile(filepath.Join(crate, name), []byte(data), 0600); err != nil {
						return err
					}
				}
				cmd := b.command(ctx, cargo, "install", "--offline", "--path", crate, "--root", filepath.Join(root, "installed"), "--target-dir", filepath.Join(root, "target"))
				cmd.Stdout, cmd.Stderr = os.Stdout, os.Stdout
				if scenario == "canceled" {
					if err := cmd.Start(); err != nil {
						return err
					}
					done := make(chan error, 1)
					go func() { done <- cmd.Wait() }()
					ticker := time.NewTicker(25 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case err := <-done:
							return fmt.Errorf("build stopped before cancellation: %v", err)
						case <-ctx.Done():
							<-done
							return ctx.Err()
						case <-ticker.C:
							if _, err := os.Stat(marker); err == nil {
								cancel()
								if err := <-done; err == nil {
									return errors.New("build did not cancel")
								}
								return context.Canceled
							}
						}
					}
				}
				if err := b.run(cmd); err != nil {
					return err
				}
				installed = filepath.Join(root, "installed", "bin", "spool-cleanup-check")
				return nil
			}()
			if !prepared {
				t.Fatalf("temporary Rust was not prepared: %v", err)
			}
			if scenario == "success" && err != nil || scenario != "success" && err == nil {
				t.Fatalf("unexpected %s result: %v", scenario, err)
			}
			if scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if b.rustRoot == "" {
				t.Fatal("temporary bootstrap never started")
			}
			if _, err := os.Stat(b.rustRoot); !os.IsNotExist(err) {
				t.Fatalf("temporary Rust was retained: %v", err)
			}
			for _, dir := range []string{".cargo", ".rustup"} {
				if _, err := os.Stat(filepath.Join(home, dir)); !os.IsNotExist(err) {
					t.Fatalf("bootstrap wrote outside its private homes: %s %v", dir, err)
				}
			}
			if installed != "" {
				output, err := exec.CommandContext(t.Context(), installed).CombinedOutput()
				if err != nil || strings.TrimSpace(string(output)) != "temporary-rust-build-ok" {
					t.Fatalf("installed binary depends on deleted toolchain: %s %v", output, err)
				}
			}
			t.Logf("%s: temporary Rust removed; isolated HOME clean", scenario)
		})
	}
}
