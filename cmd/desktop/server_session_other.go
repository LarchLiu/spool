//go:build !darwin

package main

func persistentDesktopSessions() bool { return true }
