package main

import (
	"path/filepath"
	"testing"

	"spool/internal/workspace"
)

func TestProjectMemberUploadPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	alice := decode[workspace.User](t, cliOK(t, root, "user", "add", "--username", "alice"))
	bob := decode[workspace.User](t, cliOK(t, root, "user", "add", "--username", "bob"))
	project := decode[localProject](t, cliOK(t, root, "project", "add", "--name", "Example", "--directory", t.TempDir(), "--members", "alice"))
	if project.MemberUpload[alice.ID] {
		t.Fatal("new project members must default to uploads disabled")
	}
	for _, step := range []struct {
		name       string
		command    []string
		alice, bob bool
		access     string
		own        string
	}{
		{"grant on joining with read-only chat permissions", []string{"project", "member", "set", "--project", project.ID, "--user", "bob", "--access", "read-only", "--own-access", "read-only", "--upload=true"}, false, true, "read-only", "read-only"},
		{"preserve when omitted", []string{"project", "member", "set", "--project", project.ID, "--user", "bob"}, false, true, "read-only", "read-only"},
		{"preserve while changing chat permissions", []string{"project", "member", "set", "--project", project.ID, "--user", "bob", "--access", "control"}, false, true, "control", "read-only"},
		{"preserve during project edits", []string{"project", "update", "--project", project.ID, "--name", "Renamed"}, false, true, "control", "read-only"},
		{"preserve when replacing the member list", []string{"project", "update", "--project", project.ID, "--members", "alice,bob"}, false, true, "control", "read-only"},
		{"revoke explicitly", []string{"project", "member", "set", "--project", project.ID, "--user", "bob", "--upload=false"}, false, false, "control", "read-only"},
		{"preserve disabled permission when omitted", []string{"project", "member", "set", "--project", project.ID, "--user", "bob"}, false, false, "control", "read-only"},
		{"grant an existing member", []string{"project", "member", "set", "--project", project.ID, "--user", "alice", "--upload=true"}, true, false, "control", "read-only"},
		{"grant again without changing other members", []string{"project", "member", "set", "--project", project.ID, "--user", "bob", "--upload=true"}, true, true, "control", "read-only"},
		{"remove membership", []string{"project", "member", "remove", "--project", project.ID, "--user", "bob"}, true, false, "", ""},
		{"rejoining does not restore the grant", []string{"project", "member", "set", "--project", project.ID, "--user", "bob"}, true, false, "control", "create"},
	} {
		if !t.Run(step.name, func(t *testing.T) {
			cliOK(t, root, step.command...)
			// Read back through a separate command and store to verify persistence
			// and the authorization used by the upload endpoints.
			saved := decode[localProject](t, cliOK(t, root, "project", "show", "--project", project.ID))
			if saved.MemberUpload[alice.ID] != step.alice || saved.MemberUpload[bob.ID] != step.bob || saved.MemberAccess[bob.ID] != step.access || saved.MemberOwnAccess[bob.ID] != step.own {
				t.Fatalf("saved permissions: uploads=%v shared=%v own=%v", saved.MemberUpload, saved.MemberAccess, saved.MemberOwnAccess)
			}
			service, err := workspace.OpenAdministration(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := service.Close(); err != nil {
					t.Error(err)
				}
			})
			if service.Store.CanUploadProject(alice.ID, project.ID) != step.alice || service.Store.CanUploadProject(bob.ID, project.ID) != step.bob {
				t.Fatal("effective upload permissions differ from the saved grants")
			}
		}) {
			return
		}
	}
}
