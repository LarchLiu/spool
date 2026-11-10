package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"spool/internal/workspace"
)

// Project's API JSON deliberately hides local paths and membership. The local
// console must include these fields so administrators can inspect permissions.
type localProject struct {
	workspace.Project
	Path            string                     `json:"path"`
	Members         []string                   `json:"members"`
	MemberAccess    map[string]string          `json:"member_access"`
	MemberOwnAccess map[string]string          `json:"member_own_access"`
	Issues          workspace.IssuesAutomation `json:"issues"`
}

func projectJSON(p workspace.Project) localProject {
	return localProject{p, p.Path, p.Members, p.MemberAccess, p.MemberOwnAccess, p.Issues}
}

func findProject(s *workspace.Store, ref string) (workspace.Project, error) {
	projects, err := s.Projects()
	if err != nil {
		return workspace.Project{}, err
	}
	for _, p := range projects {
		if p.ID == ref {
			return p, nil
		}
	}
	var found []workspace.Project
	for _, p := range projects {
		if strings.EqualFold(p.Name, ref) {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) > 1 {
		return workspace.Project{}, fmt.Errorf("ambiguous project name %q; use an ID from 'project list'", ref)
	}
	return workspace.Project{}, fmt.Errorf("unknown project %q (use 'project list')", ref)
}

func userIDs(s *workspace.Store, names string, active bool) ([]string, error) {
	ids := []string{}
	if strings.TrimSpace(names) == "" {
		return ids, nil
	}
	for _, ref := range strings.Split(names, ",") {
		u, err := findUser(s, strings.TrimSpace(ref))
		if err != nil {
			return nil, err
		}
		if active && u.Disabled {
			return nil, fmt.Errorf("user %q is disabled", u.Username)
		}
		if !slices.Contains(ids, u.ID) {
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

func addProjects(root *command, out io.Writer) {
	c := root.add("project", "Manage project directories, sharing and member permissions", "Project selectors accept an ID or an unambiguous name. Run while the service is stopped.\nRemoving membership does not delete files or chat history.")
	list := c.add("list", "List all projects, paths, members and permissions", "Includes archived projects.")
	list.run = func(_ context.Context, s *workspace.Service) error {
		all, err := s.Store.Projects()
		if err != nil {
			return err
		}
		v := []localProject{}
		for _, p := range all {
			v = append(v, projectJSON(p))
		}
		return writeJSON(out, v)
	}
	show := c.add("show", "Inspect one project", "")
	ref := show.flags.String("project", "", "Project name or ID (required)")
	show.validate = func() error { return required("project", *ref) }
	show.run = func(_ context.Context, s *workspace.Service) error {
		p, err := findProject(s.Store, *ref)
		if err != nil {
			return err
		}
		return writeJSON(out, projectJSON(p))
	}
	for _, action := range []string{"add", "update"} {
		n := c.add(action, strings.Title(action)+" a project", "Omitted update options preserve saved values. --members replaces the member list;\nuse 'project member set/remove' for individual changes. New members can control\nshared chats and create personal chats by default. A directory cannot change once chats exist.")
		ref := ""
		if action == "update" {
			n.flags.StringVar(&ref, "project", "", "Project name or ID (required)")
		}
		name := n.flags.String("name", "", "Project display name (required for add)")
		path := n.flags.String("directory", "", "Existing local project directory (required for add)")
		members := n.flags.String("members", "", "Comma-separated usernames/IDs; empty clears membership")
		shared := n.flags.Bool("shared-chats", false, "Share Spool chats with project members")
		local := n.flags.Bool("shared-local-chats", false, "Share discovered native CLI sessions with project members")
		external := n.flags.Bool("allow-external-files", true, "Allow supported external file references")
		n.validate = func() error {
			if action == "add" {
				return required("name", *name, "directory", *path)
			}
			if err := required("project", ref); err != nil {
				return err
			}
			if n.flags.NFlag() < 2 {
				return usage("supply at least one project field to update")
			}
			return nil
		}
		n.run = func(_ context.Context, s *workspace.Service) error {
			p := workspace.Project{AllowExternalFiles: true}
			var err error
			if action == "update" {
				p, err = findProject(s.Store, ref)
				if err != nil {
					return err
				}
			}
			if changed(n, "name") {
				p.Name = *name
			}
			if changed(n, "directory") {
				p.Path = *path
			}
			if changed(n, "members") {
				p.Members, err = userIDs(s.Store, *members, false)
				if err != nil {
					return err
				}
			}
			if changed(n, "shared-chats") {
				p.SharedChats = *shared
			}
			if changed(n, "shared-local-chats") {
				p.SharedLocalChats = *local
			}
			if changed(n, "allow-external-files") {
				p.AllowExternalFiles = *external
			}
			p, err = s.Store.SaveProject(p)
			if err != nil {
				return err
			}
			return writeJSON(out, projectJSON(p))
		}
	}
	for _, action := range []string{"archive", "unarchive"} {
		n := c.add(action, strings.Title(action)+" a project", "Archiving removes client access and pauses its work; files/history are retained.")
		ref := n.flags.String("project", "", "Project name or ID (required)")
		n.validate = func() error { return required("project", *ref) }
		n.run = func(_ context.Context, s *workspace.Service) error {
			p, err := findProject(s.Store, *ref)
			if err != nil {
				return err
			}
			p.Archived = action == "archive"
			p, err = s.Store.SaveProject(p)
			if err != nil {
				return err
			}
			return writeJSON(out, projectJSON(p))
		}
	}
	m := c.add("member", "Grant, edit or remove individual project membership", "")
	for _, action := range []string{"set", "remove"} {
		n := m.add(action, strings.Title(action)+" a project member", "Shared access: control or read-only. Own-chat access: create or read-only.\nExisting member permissions are preserved when their options are omitted.\nAn automation owner/assignee must remain a member while automation is enabled.")
		ref := n.flags.String("project", "", "Project name or ID (required)")
		user := n.flags.String("user", "", "Username or ID (required)")
		access, own := "", ""
		if action == "set" {
			n.flags.StringVar(&access, "access", "control", "Shared-chat permission: control or read-only")
			n.flags.StringVar(&own, "own-access", "create", "Personal-chat permission: create or read-only")
		}
		n.validate = func() error {
			if err := required("project", *ref, "user", *user); err != nil {
				return err
			}
			if action == "set" && (access != "control" && access != "read-only" || own != "create" && own != "read-only") {
				return usage("invalid --access or --own-access; use '%s --help'", n.path)
			}
			return nil
		}
		n.run = func(_ context.Context, s *workspace.Service) error {
			p, err := findProject(s.Store, *ref)
			if err != nil {
				return err
			}
			u, err := findUser(s.Store, *user)
			if err != nil {
				return err
			}
			if action == "remove" {
				p.Members = slices.DeleteFunc(p.Members, func(id string) bool { return id == u.ID })
			} else {
				exists := slices.Contains(p.Members, u.ID)
				if !exists {
					p.Members = append(p.Members, u.ID)
				}
				if !exists || changed(n, "access") {
					p.MemberAccess[u.ID] = access
				}
				if !exists || changed(n, "own-access") {
					p.MemberOwnAccess[u.ID] = own
				}
			}
			p, err = s.Store.SaveProject(p)
			if err != nil {
				return err
			}
			return writeJSON(out, projectJSON(p))
		}
	}
}
