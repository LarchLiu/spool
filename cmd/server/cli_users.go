package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"spool/internal/workspace"
)

func findUser(s *workspace.Store, ref string) (workspace.User, error) {
	users, err := s.Users()
	if err != nil {
		return workspace.User{}, err
	}
	for _, u := range users {
		if u.ID == ref {
			return u, nil
		}
	}
	for _, u := range users {
		if strings.EqualFold(u.Username, ref) {
			return u, nil
		}
	}
	return workspace.User{}, fmt.Errorf("unknown user %q (use 'user list')", ref)
}

func userInput(u workspace.User) workspace.UserInput {
	return workspace.UserInput{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Disabled: u.Disabled, RemoteDesktop: u.RemoteDesktop}
}

func addUsers(root *command, out io.Writer) {
	c := root.add("user", "List, create and update local login accounts", "Run while the service is stopped. Password changes read SPOOL_NEW_PASSWORD (12-72 bytes).\nUpdating an account revokes its existing login sessions. Disabled accounts retain history.")
	list := c.add("list", "List accounts including disabled users", "")
	list.run = func(_ context.Context, s *workspace.Service) error {
		v, err := s.Store.Users()
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
	add := c.add("add", "Create a login account", "Set SPOOL_NEW_PASSWORD in the environment; passwords are never accepted as command arguments.\nExample: SPOOL_NEW_PASSWORD='a-long-unique-password' spool-server user add --username alice")
	username := add.flags.String("username", "", "Unique login name (required)")
	nickname := add.flags.String("nickname", "", "Display name; defaults to username")
	add.validate = func() error {
		if err := required("username", *username); err != nil {
			return err
		}
		_, err := envRequired("SPOOL_NEW_PASSWORD")
		return err
	}
	add.run = func(_ context.Context, s *workspace.Service) error {
		name := *nickname
		if name == "" {
			name = *username
		}
		password, _ := envRequired("SPOOL_NEW_PASSWORD")
		v, err := s.Store.SaveUser(workspace.UserInput{Username: *username, Nickname: name, Password: password})
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
	update := c.add("update", "Change a username, display name or password", "Only supplied fields change. Use --password to read the new password from SPOOL_NEW_PASSWORD.")
	ref := update.flags.String("user", "", "Current username or ID (required)")
	newName := update.flags.String("username", "", "New login name")
	newNick := update.flags.String("nickname", "", "New display name")
	password := update.flags.Bool("password", false, "Reset password from SPOOL_NEW_PASSWORD")
	update.validate = func() error {
		if err := required("user", *ref); err != nil {
			return err
		}
		if !changed(update, "username") && !changed(update, "nickname") && !*password {
			return usage("supply --username, --nickname or --password")
		}
		if *password {
			_, err := envRequired("SPOOL_NEW_PASSWORD")
			return err
		}
		return nil
	}
	update.run = func(_ context.Context, s *workspace.Service) error {
		u, err := findUser(s.Store, *ref)
		if err != nil {
			return err
		}
		v := userInput(u)
		if changed(update, "username") {
			v.Username = *newName
		}
		if changed(update, "nickname") {
			v.Nickname = *newNick
		}
		if *password {
			v.Password, _ = envRequired("SPOOL_NEW_PASSWORD")
		}
		saved, err := s.Store.SaveUser(v)
		if err != nil {
			return err
		}
		return writeJSON(out, saved)
	}
	for _, action := range []string{"enable", "disable"} {
		n := c.add(action, strings.Title(action)+" a login account", "Retains projects and chat history; existing login sessions are revoked.")
		ref := n.flags.String("user", "", "Username or ID (required)")
		n.validate = func() error { return required("user", *ref) }
		n.run = func(_ context.Context, s *workspace.Service) error {
			u, err := findUser(s.Store, *ref)
			if err != nil {
				return err
			}
			v := userInput(u)
			v.Disabled = action == "disable"
			saved, err := s.Store.SaveUser(v)
			if err != nil {
				return err
			}
			return writeJSON(out, saved)
		}
	}
}
