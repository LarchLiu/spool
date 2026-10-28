package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"spool/internal/issues"
	"spool/internal/workspace"
)

func TestIssueLabelsCanBeAddedEditedAndRemovedInProjectSettings(t *testing.T) {
	p := &panel{
		preferences: workspace.UIPreferences{Language: "en"},
		project:     workspace.Project{Path: "/project", Issues: workspace.IssuesAutomation{Enabled: true, Labels: []string{"web"}}},
		projectGit:  githubRepositoryCheck{directory: "/project", repository: "fixture/issues"},
		github:      githubCLICheck{status: issues.CLIStatus{State: issues.CLIReady}},
	}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
	}, 900, 1500)
	if !view.HasText("Issue labels") || !view.HasText("Add label") || !view.HasText("Remove label") {
		t.Fatal("label controls missing")
	}
	if err := view.Click("New issue label"); err != nil {
		t.Fatal(err)
	}
	view.Type("自定义 / v2,urgent")
	if err := view.Click("Add label"); err != nil {
		t.Fatal(err)
	}
	if len(p.project.Issues.Labels) != 2 || p.project.Issues.Labels[1] != "自定义 / v2,urgent" || p.issueLabel != "" {
		t.Fatalf("custom label was not added: %#v", p.project.Issues.Labels)
	}
	if err := view.Click("Remove label"); err != nil {
		t.Fatal(err)
	}
	if len(p.project.Issues.Labels) != 1 {
		t.Fatal("label was not removed")
	}
}

func TestIssueLabelsDefaultToEmptyAndCanRemoveLastLabel(t *testing.T) {
	p := &panel{
		preferences: workspace.UIPreferences{Language: "en"},
		project:     workspace.Project{Path: "/project", Issues: workspace.IssuesAutomation{Enabled: true}},
		projectGit:  githubRepositoryCheck{directory: "/project", repository: "fixture/issues"},
		github:      githubCLICheck{status: issues.CLIStatus{State: issues.CLIReady}},
	}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
	}, 900, 1500)
	if len(p.project.Issues.Labels) != 0 || view.HasText("Remove label") || !view.HasText("Add label") {
		t.Fatal("default settings did not leave labels empty")
	}
	if err := view.Click("New issue label"); err != nil {
		t.Fatal(err)
	}
	view.Type("custom")
	if err := view.Click("Add label"); err != nil {
		t.Fatal(err)
	}
	if len(p.project.Issues.Labels) != 1 {
		t.Fatal("could not add a label to unfiltered settings")
	}
	if err := view.Click("Remove label"); err != nil {
		t.Fatal(err)
	}
	if len(p.project.Issues.Labels) != 0 || view.HasText("Remove label") {
		t.Fatal("removing the last label restored default filters")
	}
}
