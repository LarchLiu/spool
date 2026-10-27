package agentfixture

import "testing"

// PID identifies the most recently spawned CLI in a fixture directory.
func PID(t *testing.T, directory string) int {
	t.Helper()
	entries := Log(t, directory)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["method"] == "spawn" {
			if pid, ok := entries[i]["pid"].(float64); ok && pid > 0 {
				return int(pid)
			}
		}
	}
	t.Fatal("fixture CLI process not found")
	return 0
}
