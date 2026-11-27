package main

import "testing"

func TestBackgroundActivityReleasesWhenIdleAndOnShutdown(t *testing.T) {
	var activity desktopBackgroundActivity
	acquired, released := 0, 0
	acquire := func() func() {
		acquired++
		return func() { released++ }
	}
	activity.update(false, acquire)
	activity.update(true, acquire)
	activity.update(true, acquire)
	if acquired != 1 || released != 0 {
		t.Fatalf("repeated status refresh duplicated activity: %d / %d", acquired, released)
	}
	activity.update(false, acquire)
	activity.update(false, acquire)
	activity.update(true, acquire)
	activity.close()
	activity.close()
	if acquired != 2 || released != 2 {
		t.Fatalf("background activity leaked across idle/shutdown: %d / %d", acquired, released)
	}
}
