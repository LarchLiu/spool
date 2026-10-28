package issues

import "testing"

func TestSelectionWithoutLabelsIgnoresIssueLabelsAndReservesProject(t *testing.T) {
	for _, labels := range [][]string{nil, {}} {
		for _, issueLabels := range [][]string{nil, {"web"}, {"arbitrary", "other"}} {
			snapshot := []Issue{testIssue(1, "open", issueLabels...), testIssue(2, "open", "different")}
			l := &Ledger{Records: map[int]*Record{}}
			l.Sync(snapshot, labels)
			selected, resume := l.Select(snapshot, labels, func(Owner) bool { return false })
			if selected == nil || selected.Number != 1 || resume || len(l.Records[1].Categories) != 0 {
				t.Fatal("empty settings did not select the first issue without filtering")
			}
			l.Records[1].Status = "processing"
			l.Records[1].Owner = NewOwner("instance", "live")
			for _, status := range []string{"processing", "pr", "close"} {
				l.Records[1].Status = status
				if selected, _ := l.Select(snapshot, labels, func(Owner) bool { return false }); selected != nil {
					t.Fatalf("live global task (%s) did not reserve the project", status)
				}
			}
			l.Records[1].Status = "processing"
			selected, resume = l.Select(snapshot, labels, func(Owner) bool { return true })
			if selected == nil || selected.Number != 1 || !resume {
				t.Fatal("ended global task was not resumed first")
			}
		}
	}
}

func TestSelectionSwitchingLabelModesKeepsLiveReservations(t *testing.T) {
	snapshot := []Issue{testIssue(1, "open", "alpha"), testIssue(2, "open", "beta")}
	labels := []string{"alpha", "beta"}
	l := &Ledger{Records: map[int]*Record{}}
	l.Sync(snapshot, labels)
	l.Records[1].Status = "processing"
	l.Records[1].Owner = NewOwner("instance", "live")
	l.Records[1].Branch, l.Records[1].Worktree = "codex/issue-1-fix", "/isolated/1"
	l.Sync(snapshot, nil)
	if selected, _ := l.Select(snapshot, nil, func(Owner) bool { return false }); selected != nil {
		t.Fatal("global mode overlapped an existing label task")
	}
	l.Records[1].Categories = []string{}
	for _, state := range []string{"open", "closed"} {
		snapshot[0].State = state
		l.Sync(snapshot, labels)
		if len(l.Records[1].Categories) != 0 {
			t.Fatal("settings change lost a live global reservation")
		}
		if selected, _ := l.Select(snapshot, labels, func(Owner) bool { return false }); selected != nil {
			t.Fatal("new label slots overlapped the global task")
		}
	}
	snapshot[0].State = "open"
	l.Sync(snapshot, labels)
	selected, resume := l.Select(snapshot, labels, func(Owner) bool { return true })
	if selected == nil || selected.Number != 1 || !resume {
		t.Fatal("ended global claim could not resume with configured labels")
	}
}

func TestSelectionWithoutLabelsSeriallyResumesMultipleEndedClaims(t *testing.T) {
	snapshot := []Issue{testIssue(1, "open", "alpha"), testIssue(2, "open", "beta"), testIssue(3, "open")}
	l := &Ledger{Records: map[int]*Record{}}
	l.Sync(snapshot, []string{"alpha", "beta"})
	for _, n := range []int{1, 2} {
		l.Records[n].Status = "processing"
		l.Records[n].Owner = NewOwner("instance", "ended")
	}
	l.Sync(snapshot, nil)
	ended := func(o Owner) bool { return o.Run == "ended" }
	selected, resume := l.Select(snapshot, nil, ended)
	if selected == nil || selected.Number != 1 || !resume {
		t.Fatal("switching to serial mode deadlocked ended label claims")
	}
	l.Records[1].Categories = []string{}
	l.Records[1].Owner = NewOwner("instance", "live")
	if selected, _ := l.Select(snapshot, nil, ended); selected != nil {
		t.Fatal("resumed global task did not block the other ended claim")
	}
	l.Records[1].Status, l.Records[1].Owner = "pr", Owner{}
	selected, resume = l.Select(snapshot, nil, ended)
	if selected == nil || selected.Number != 2 || !resume {
		t.Fatal("remaining ended label claim was not resumed")
	}
	l.Records[2].Owner = Owner{Automation: "human"}
	if selected, _ := l.Select(snapshot, nil, func(Owner) bool { return true }); selected != nil {
		t.Fatal("global mode bypassed an unresolved human claim")
	}
}

func TestSelectionNewLabelsDoNotStayBlockedByEndedGlobalClaim(t *testing.T) {
	snapshot := []Issue{testIssue(1, "open", "alpha"), testIssue(2, "open", "beta")}
	l := &Ledger{Records: map[int]*Record{}}
	l.Sync(snapshot, nil)
	l.Records[1].Status = "processing"
	l.Records[1].Owner = NewOwner("instance", "old-global")
	l.Sync(snapshot, []string{"beta"})
	if selected, _ := l.Select(snapshot, []string{"beta"}, func(Owner) bool { return false }); selected != nil {
		t.Fatal("new label task bypassed a live global executor")
	}
	selected, resume := l.Select(snapshot, []string{"beta"}, func(Owner) bool { return true })
	if selected == nil || selected.Number != 2 || resume {
		t.Fatal("ended global claim still blocked the newly configured label")
	}
	l.Records[1].Owner.Automation = "human"
	if selected, _ := l.Select(snapshot, []string{"beta"}, func(Owner) bool { return true }); selected != nil {
		t.Fatal("new labels bypassed an unresolved human claim")
	}
}
