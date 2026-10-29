package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func localGitWorktree(t *testing.T, repository, directory string) {
	t.Helper()
	git := filepath.Join(repository, ".git", "worktrees", chatmodel.NewID())
	if err := os.MkdirAll(git, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(git, "commondir"), []byte("../..\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".git"), []byte("gitdir: "+git+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSharingProtectsPrivateWorktreeProjects(t *testing.T) {
	for _, layout := range []string{"mirrored-nested-project", "private-repository-in-shared-parent"} {
		t.Run(layout, func(t *testing.T) {
			localSessionHomes(t)
			s := testStore(t)
			owner, viewer := testUser(t, s, "owner"), testUser(t, s, "viewer")
			shared := testProject(t, s, owner, viewer)
			shared.SharedChats, shared.SharedLocalChats = true, true
			if _, err := s.SaveProject(shared); err != nil {
				t.Fatal(err)
			}
			var secret string
			if layout == "mirrored-nested-project" {
				linked := t.TempDir()
				localGitWorktree(t, shared.Path, linked)
				private := filepath.Join(shared.Path, "private")
				secret = filepath.Join(linked, "private")
				for _, directory := range []string{private, secret} {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.SaveProject(Project{Name: "Private nested project", Path: private, Members: []string{owner.ID}}); err != nil {
					t.Fatal(err)
				}
			} else {
				private := testProject(t, s, owner)
				secret = filepath.Join(shared.Path, "linked-private-repository")
				localGitWorktree(t, private.Path, secret)
			}
			writeLocalSession(t, chatmodel.BackendCodex, "private-worktree-session", secret, 1)
			writeLocalSession(t, chatmodel.BackendCodex, "public-session", shared.Path, 2)
			nav, err := s.UserNavigation(viewer.ID)
			if err != nil || len(nav.Chats) != 1 {
				t.Fatalf("private worktree leaked or public session disappeared: %+v %v", nav.Chats, err)
			}
			chat, ok := s.getSession(nav.Chats[0].ID)
			if !ok || chat.NativeSessionID != "public-session" || chat.ProjectID != shared.ID {
				t.Fatalf("incorrect worktree project mapping: %+v", chat)
			}
		})
	}
}

func TestLocalSharingWorktreeAmbiguityAndExplicitCheckouts(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "owner")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	linked, outside := t.TempDir(), t.TempDir()
	localGitWorktree(t, p.Path, linked)
	localGitWorktree(t, p.Path, outside)
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveProject(Project{Name: "Configured worktree", Path: linked, Members: []string{u.ID}, SharedChats: true, SharedLocalChats: true})
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSession(t, chatmodel.BackendCodex, "primary-checkout", p.Path, 1)
	writeLocalSession(t, chatmodel.BackendCodex, "configured-checkout", linked, 2)
	writeLocalSession(t, chatmodel.BackendCodex, "ambiguous-checkout", outside, 3)
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 2 {
		t.Fatalf("ambiguous worktree did not fail closed: %+v %v", nav.Chats, err)
	}
	for _, chat := range nav.Chats {
		stored, _ := s.getSession(chat.ID)
		want := p.ID
		if stored.NativeSessionID == "configured-checkout" {
			want = other.ID
		}
		if stored.ProjectID != want {
			t.Fatalf("explicit checkout mapping lost to another worktree: %+v", stored)
		}
	}
}

func TestLocalSharingSubdirectoryDoesNotPublishOtherCheckouts(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "owner")
	repository, linked := t.TempDir(), t.TempDir()
	localGitWorktree(t, repository, linked)
	for _, root := range []string{repository, linked} {
		if err := os.MkdirAll(filepath.Join(root, "allowed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.SaveProject(Project{Name: "Subdirectory only", Path: filepath.Join(repository, "allowed"), Members: []string{u.ID}, SharedChats: true, SharedLocalChats: true})
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSession(t, chatmodel.BackendCodex, "direct-subdirectory", p.Path, 1)
	writeLocalSession(t, chatmodel.BackendCodex, "linked-subdirectory", filepath.Join(linked, "allowed"), 2)
	writeLocalSession(t, chatmodel.BackendCodex, "linked-sibling", linked, 3)
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 1 {
		t.Fatalf("a subdirectory opted into other checkouts: %+v %v", nav.Chats, err)
	}
	stored, _ := s.getSession(nav.Chats[0].ID)
	if stored.NativeSessionID != "direct-subdirectory" {
		t.Fatalf("direct subdirectory access was lost: %+v", stored)
	}
}

func TestLocalSharingUnreadableProviderDoesNotBlockNavigation(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			localSessionHomes(t)
			s := testStore(t)
			owner, viewer := testUser(t, s, "owner"), testUser(t, s, "viewer")
			shared := testProject(t, s, owner)
			shared.SharedChats, shared.SharedLocalChats = true, true
			if _, err := s.SaveProject(shared); err != nil {
				t.Fatal(err)
			}
			own := testProject(t, s, viewer)
			personal, err := s.CreateChat(viewer.ID, own.ID, chatmodel.BackendCodex)
			if err != nil {
				t.Fatal(err)
			}
			path := writeLocalSession(t, backend, "unreadable-local", shared.Path, 1)
			locked := filepath.Dir(path)
			if err := os.Chmod(locked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
			if _, err := os.ReadDir(locked); !os.IsPermission(err) {
				t.Skip("cannot simulate an unreadable directory")
			}
			healthy := chatmodel.BackendCodex
			if backend == healthy {
				healthy = chatmodel.BackendClaude
			}
			writeLocalSession(t, healthy, "healthy-local", shared.Path, 2)
			nav, err := s.UserNavigation(viewer.ID)
			if err != nil || len(nav.Chats) != 1 || nav.Chats[0].ID != personal.ID {
				t.Fatalf("unrelated provider blocked personal navigation: %+v %v", nav, err)
			}
			chats, err := s.UserSessions(viewer.ID, own.ID)
			if err != nil || len(chats) != 1 || chats[0].ID != personal.ID {
				t.Fatalf("unrelated provider blocked project sessions: %+v %v", chats, err)
			}
			nav, err = s.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 1 {
				t.Fatalf("healthy provider stopped being discovered: %+v %v", nav, err)
			}
			if err := os.Chmod(locked, 0700); err != nil {
				t.Fatal(err)
			}
			if err := s.syncLocalSessions(true); err != nil {
				t.Fatal(err)
			}
			nav, err = s.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 2 {
				t.Fatalf("provider did not recover after permissions were restored: %+v %v", nav, err)
			}
		})
	}
}

func TestLocalSharingSampledTitleDoesNotOverwriteHydratedTitle(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	u := testUser(t, s, "owner")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	path := writeLocalSession(t, chatmodel.BackendClaude, "renamed-local", p.Path, 1)
	padding := map[string]any{"type": "progress", "data": strings.Repeat("x", 16000)}
	for range 22 {
		appendTitleRecord(t, path, padding)
	}
	appendTitleRecord(t, path, map[string]any{"type": "custom-title", "customTitle": "Actual renamed title"})
	for range 8 {
		appendTitleRecord(t, path, padding)
	}
	nav, err := s.UserNavigation(u.ID)
	if err != nil || len(nav.Chats) != 1 {
		t.Fatalf("missing local session: %+v %v", nav, err)
	}
	hydrated, ok := s.Get(nav.Chats[0].ID)
	if !ok || hydrated.Title != "Actual renamed title" {
		t.Fatalf("complete history did not supply the title: %+v", hydrated)
	}
	sub := s.changes.subscribe()
	defer sub.close()
	for range 3 {
		if err := s.syncLocalSessions(true); err != nil {
			t.Fatal(err)
		}
		indexed, ok := s.getSession(hydrated.ID)
		if !ok || indexed.Title != hydrated.Title || indexed.UpdatedAt != hydrated.UpdatedAt {
			t.Fatalf("unchanged sample reverted the title or advanced activity: %+v", indexed)
		}
	}
	if sub.take() != 0 {
		t.Fatal("unchanged title sample published a navigation update")
	}
	appendTitleRecord(t, path, padding)
	if err := s.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	indexed, _ := s.getSession(hydrated.ID)
	if indexed.Title != hydrated.Title || indexed.UpdatedAt <= hydrated.UpdatedAt {
		t.Fatalf("new activity lost a confirmed title: %+v", indexed)
	}
	indexes, reads := countNativeHistoryReads(s)
	appendTitleRecord(t, path, map[string]any{"type": "custom-title", "customTitle": "Newest tail title"})
	if err := s.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	indexed, _ = s.getSession(hydrated.ID)
	if indexed.Title != "Newest tail title" || indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("authoritative tail rename was lost or required full history: %+v", indexed)
	}
}
