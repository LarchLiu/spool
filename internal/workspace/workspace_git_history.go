package workspace

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const workspaceGitHistoryPage = 50

// Anchor subsequent pages to the original HEAD so a new commit cannot shift
// page boundaries. The caller verifies that the anchor is still reachable.
func workspaceGitHistory(ctx context.Context, directory string, view *workspaceGitView, anchor string, offset int) error {
	run := func(args ...string) ([]byte, error) { return readWorkspaceGit(ctx, directory, 4*1024*1024, args...) }
	args := []string{"log", "-" + strconv.Itoa(workspaceGitHistoryPage+1), "--skip=" + strconv.Itoa(offset), "-z", "--format=%H%x00%s%x00%an%x00%at%x00%B", anchor, "--"}
	// A path filter drops empty release commits and their tags. Only scope
	// history by path when viewing a subdirectory of the selected worktree.
	if root, _ := gitWorkspace(directory); directory != root {
		args = append(args, ".")
	}
	body, err := run(args...)
	if err != nil {
		return err
	}
	fields := strings.Split(string(body), "\x00")
	for i := 0; i+4 < len(fields); i += 5 {
		if len(view.Commits) == workspaceGitHistoryPage {
			view.NextCursor = anchor + ":" + strconv.Itoa(offset+workspaceGitHistoryPage)
			break
		}
		stamp, _ := strconv.ParseInt(fields[i+3], 10, 64)
		view.Commits = append(view.Commits, workspaceGitCommit{ID: strings.TrimSpace(fields[i]), Subject: fields[i+1], Author: fields[i+2], Time: stamp, Message: strings.TrimRight(fields[i+4], "\r\n")})
	}
	return workspaceGitCommitBadges(ctx, directory, view, anchor, view.Commits)
}

func workspaceGitCommitDetail(ctx context.Context, directory string, view *workspaceGitView, id string) (*workspaceGitCommit, error) {
	body, err := readWorkspaceGit(ctx, directory, 4*1024*1024, "show", "--no-patch", "--format=%H%x00%s%x00%an%x00%at%x00%B", id, "--")
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(body), "\x00", 5)
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid Git commit information")
	}
	stamp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return nil, err
	}
	items := []workspaceGitCommit{{ID: parts[0], Subject: parts[1], Author: parts[2], Time: stamp, Message: strings.TrimRight(parts[4], "\r\n")}}
	if err := workspaceGitCommitBadges(ctx, directory, view, id, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

func workspaceGitCommitBadges(ctx context.Context, directory string, view *workspaceGitView, anchor string, commits []workspaceGitCommit) error {
	if len(commits) == 0 {
		return nil
	}
	run := func(args ...string) ([]byte, error) { return readWorkspaceGit(ctx, directory, 4*1024*1024, args...) }
	// Filter tags to this page, retaining exact tag names (including commas) and
	// both lightweight and annotated tags. No parsing of human-facing decorations.
	args := []string{"for-each-ref", "--format=%(objectname)%00%(*objectname)%00%(refname:strip=2)"}
	for _, item := range commits {
		args = append(args, "--points-at="+item.ID)
	}
	args = append(args, "refs/tags/")
	body, err := run(args...)
	if err != nil {
		return err
	}
	tags := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 3 {
			continue
		}
		id := parts[0]
		if parts[1] != "" {
			id = parts[1]
		}
		tags[id] = append(tags[id], parts[2])
	}
	unpushed := map[string]bool{}
	if view.Upstream != "" && view.Ahead > 0 {
		body, err = run("rev-list", anchor, "--not", "@{upstream}", "--")
		if err != nil {
			return err
		}
		for _, id := range strings.Fields(string(body)) {
			unpushed[id] = true
		}
	}
	for i := range commits {
		item := &commits[i]
		item.Tags = tags[item.ID]
		item.Unpushed = unpushed[item.ID] || view.Upstream == "" && view.Remote != "" && view.Branch != ""
	}
	return nil
}
