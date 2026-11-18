package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestClaudeCommandsDiscoverNativeMetadataAndExcludeInternalCommands(t *testing.T) {
	commands := claudeCommands(map[string]any{"commands": []any{
		map[string]any{"name": "project:check", "description": "Check project", "argumentHint": "[target]", "aliases": []any{"check", "check", "bad/name"}},
		map[string]any{"name": "compact", "builtin": true},
		map[string]any{"name": "compact", "description": "duplicate"},
		map[string]any{"name": "__internal"}, map[string]any{"name": "agents", "description": "(removed) Old interface"}, map[string]any{"name": "bad command"},
	}})
	if len(commands) != 3 {
		t.Fatalf("unexpected commands: %#v", commands)
	}
	custom, ok := chatmodel.FindSlashCommand(commands, "check")
	if !ok || custom.Name != "project:check" || custom.Builtin || custom.Kind != "prompt" || custom.ArgumentHint != "[target]" || len(custom.Aliases) != 1 {
		t.Fatal(custom)
	}
}

func TestClaudeCommandsPreferBuiltinOverShadowedCustomCommand(t *testing.T) {
	builtin := map[string]any{"name": "code-review", "builtin": true, "description": "Native code review", "aliases": []any{"review"}}
	custom := map[string]any{"name": "code-review", "description": "Shadowed custom skill", "aliases": []any{"custom-review"}}
	for _, entries := range [][]any{{custom, builtin}, {builtin, custom}} {
		commands := claudeCommands(map[string]any{"commands": entries})
		review, ok := chatmodel.FindSlashCommand(commands, "review")
		if !ok || !review.Builtin || review.Description != "Native code review" {
			t.Fatal("shadowed skill replaced builtin command", commands)
		}
		if _, ok := chatmodel.FindSlashCommand(commands, "custom-review"); ok {
			t.Fatal("shadowed skill alias survived", commands)
		}
	}
}

func TestClaudeMenuKeepsFiveCommandsAndOnlyNativeConfirmedInvocableSkills(t *testing.T) {
	body := map[string]any{"commands": []any{
		map[string]any{"name": "code-review", "builtin": true, "aliases": []any{"review"}},
		map[string]any{"name": "goal", "builtin": true},
		map[string]any{"name": "loop", "builtin": true},
		map[string]any{"name": "deep-research", "builtin": true},
		map[string]any{"name": "simplify", "builtin": true, "description": "Simplify changes", "aliases": []any{"tidy"}},
		map[string]any{"name": "batch", "builtin": true},
		map[string]any{"name": "project:check", "description": "Check the project", "argumentHint": "[target]"},
		map[string]any{"name": "personal-check"},
		map[string]any{"name": "plugin:check", "plugin": "checks", "aliases": []any{"check-plugin"}},
		map[string]any{"name": "unconfirmed-custom"},
		map[string]any{"name": "doctor", "builtin": true},
		map[string]any{"name": "disabled"},
		map[string]any{"name": "model-only"},
		map[string]any{"name": "shadowed"},
		map[string]any{"name": "unknown-state"},
		map[string]any{"name": "context", "builtin": true},
		map[string]any{"name": "compact", "builtin": true},
		map[string]any{"name": "model", "builtin": true},
		map[string]any{"name": "rename", "builtin": true},
		map[string]any{"name": "__internal", "builtin": true},
		map[string]any{"name": "agents", "description": "(removed) Old interface", "builtin": true},
	}}
	dialog := map[string]any{"skills": []any{
		map[string]any{"name": "batch", "display_name": "batch", "source": "built-in", "state": "on", "advertised": true},
		map[string]any{"name": "qualified:project:check", "display_name": "project:check", "description": "Check the project skill", "source": "project", "state": "on", "advertised": true},
		map[string]any{"name": "personal-check", "source": "user", "state": "user-invocable-only", "advertised": true},
		map[string]any{"name": "plugin:check", "source": "plugin", "state": "name-only", "advertised": true},
		map[string]any{"name": "disabled", "state": "off", "advertised": true},
		map[string]any{"name": "model-only", "state": "on", "advertised": false},
		map[string]any{"name": "shadowed", "state": "on", "advertised": false},
		map[string]any{"name": "unknown-state", "advertised": true},
		map[string]any{"name": "not-in-command-catalog", "state": "on", "advertised": true},
	}}
	commands := claudeMenuCommands(body, dialog)
	var ordinary, skills []string
	for _, command := range commands {
		if command.Kind == "skill" {
			skills = append(skills, command.Name)
		} else {
			ordinary = append(ordinary, command.Name)
		}
	}
	if !reflect.DeepEqual(ordinary, []string{"code-review", "deep-research", "goal", "loop", "plan"}) || !reflect.DeepEqual(skills, []string{"batch", "personal-check", "plugin:check", "project:check"}) {
		t.Fatalf("unexpected command groups: commands=%v skills=%v", ordinary, skills)
	}
	if review, ok := chatmodel.FindSlashCommand(commands, "review"); !ok || review.Name != "code-review" || review.Kind != "prompt" {
		t.Fatal("primary command alias was lost", review)
	}
	if batch, ok := chatmodel.FindSlashCommand(commands, "batch"); !ok || !batch.Builtin || batch.Kind != "skill" || batch.Scope != "system" {
		t.Fatal("confirmed builtin skill metadata was lost", batch)
	}
	if project, ok := chatmodel.FindSlashCommand(commands, "project:check"); !ok || project.Kind != "skill" || project.ArgumentHint != "[target]" || project.Scope != "repo" || project.Description != "Check the project skill" {
		t.Fatal("project skill metadata was lost", project)
	}
	if personal, ok := chatmodel.FindSlashCommand(commands, "personal-check"); !ok || personal.Scope != "user" {
		t.Fatal("user-invocable-only personal skill was lost", personal)
	}
	if plugin, ok := chatmodel.FindSlashCommand(commands, "check-plugin"); !ok || plugin.Kind != "skill" || plugin.PluginID != "checks" {
		t.Fatal("name-only plugin skill metadata was lost", plugin)
	}
	if _, ok := chatmodel.FindSlashCommand(commands, "tidy"); ok {
		t.Fatal("unconfirmed builtin command was relabeled as a skill")
	}
	if withoutSkills := claudeMenuCommands(body, nil); len(withoutSkills) != 5 {
		t.Fatal("missing Skills response inferred skills", withoutSkills)
	}
}

func TestClaudeMenuDoesNotInventUnavailableNativeCommands(t *testing.T) {
	commands := claudeMenuCommands(map[string]any{"commands": []any{}}, nil)
	if len(commands) != 1 || commands[0].Name != "plan" || commands[0].Kind != "control" {
		t.Fatal(commands)
	}
}

func TestClaudeSavedAndProjectCatalogsUseTheSameCommandGroups(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{request.Backend: request.Binary})
	saved, err := DiscoverClaudeModels(t.Context(), request.Binary)
	if err != nil {
		t.Fatal(err)
	}
	project, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory)
	if err != nil || !reflect.DeepEqual(saved.Commands, project.Commands) {
		t.Fatalf("inconsistent command catalogs: %#v %#v %v", saved.Commands, project.Commands, err)
	}
	if len(saved.Commands) != 6 || saved.CommandSource != "native" {
		t.Fatal(saved)
	}
	skillQueries := 0
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		if nestedString(value, "request", "subtype") == "get_skills_dialog" {
			skillQueries++
		}
	}
	if skillQueries != 2 {
		t.Fatal("saved and project discovery did not query native skills")
	}
	if fixtureCount(t, request, "user") != 0 {
		t.Fatal("catalog discovery started inference")
	}
}

func TestClaudeUnsupportedSkillsDiscoveryKeepsCommandsModelsAndPermissions(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "unsupported-skills-dialog"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{request.Backend: request.Binary})
	saved, err := DiscoverClaudeModels(t.Context(), request.Binary)
	if err != nil || len(saved.Models) != 1 || len(saved.Commands) != 5 {
		t.Fatalf("model discovery failed after unsupported skill query: %#v %v", saved, err)
	}
	project, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory)
	if err != nil || len(project.Modes) == 0 || !reflect.DeepEqual(saved.Commands, project.Commands) {
		t.Fatalf("permission discovery failed after unsupported skill query: %#v %v", project, err)
	}
	for _, command := range saved.Commands {
		if command.Kind == "skill" {
			t.Fatal("unsupported skill query inferred skills", command)
		}
	}
	if fixtureCount(t, request, "user") != 0 {
		t.Fatal("unsupported skill query started inference")
	}
}

func TestNativeCodexControlCommandsDoNotStartModelTurns(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	runner := newTestRunner(t)
	for _, prompt := range []string{"/plan", "/plan"} {
		request.Prompt = prompt
		result, _ := runFixture(t, runner, request)
		request.NativeSessionID = result.SessionID
		if result.Text == "" {
			t.Fatalf("empty command output: %s", prompt)
		}
	}
	if fixtureCount(t, request, "turn/start") != 0 || fixtureCount(t, request, "thread/settings/update") != 2 {
		t.Fatal("plan started inference or missed native settings")
	}
}

func TestNativeCodexReviewStartsReviewInsteadOfPrompt(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "/review main"
	runFixture(t, newTestRunner(t), request)
	if fixtureCount(t, request, "review/start") != 1 || fixtureCount(t, request, "turn/start") != 0 {
		t.Fatal("review was sent as prompt")
	}
}

func TestCodexCatalogKeepsThreeCommandsAndDiscoversProjectSkills(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	writeFixtureSkills(t, request, []any{fixtureSkill("inspect", "/skills/inspect/SKILL.md", "repo")})
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{request.Backend: request.Binary})
	var names []string
	for _, command := range codexCommands() {
		names = append(names, command.Name)
	}
	if !reflect.DeepEqual(names, []string{"goal", "plan", "review"}) {
		t.Fatal(names)
	}
	catalog, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory)
	if err != nil || len(catalog.Commands) != 4 {
		t.Fatalf("discovery: %#v %v", catalog, err)
	}
	skill, ok := chatmodel.FindSlashCommand(catalog.Commands, "inspect")
	if !ok || skill.Kind != "skill" || skill.Scope != "repo" || skill.DisplayName != "Inspect project" || skill.Description != "Check project health" {
		t.Fatal(skill)
	}
	if fixtureCount(t, request, "turn/start") != 0 || fixtureCount(t, request, "thread/start") != 0 {
		t.Fatal("skill discovery started a chat")
	}
	if _, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory); err != nil || fixtureCount(t, request, "skills/list") != 1 {
		t.Fatal("project catalog was not cached", err)
	}
	if _, err := runner.PermissionModes(t.Context(), request.Backend, t.TempDir()); err != nil || fixtureCount(t, request, "skills/list") != 2 {
		t.Fatal("project directory was missing from cache key", err)
	}
}

func fixtureSkill(name, path, scope string) map[string]any {
	return map[string]any{"name": name, "path": path, "scope": scope, "enabled": true, "description": "Full skill instructions", "interface": map[string]any{"displayName": "Inspect project", "shortDescription": "Check project health"}}
}

func writeFixtureSkills(t *testing.T, request AgentRequest, skills []any) {
	t.Helper()
	data, err := json.Marshal(skills)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-skills.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSkillCatalogFiltersAndDisambiguatesNativeSkills(t *testing.T) {
	disabled := fixtureSkill("disabled", "/skills/disabled/SKILL.md", "user")
	disabled["enabled"] = false
	first := fixtureSkill("inspect", "/a/SKILL.md", "user")
	second := fixtureSkill("inspect", "/b/SKILL.md", "repo")
	second["pluginId"] = "workspace-tools"
	items := []any{first, second, first, disabled, fixtureSkill("goal", "/goal/SKILL.md", "system"), fixtureSkill("bad/name", "/bad/SKILL.md", "repo")}
	response := map[string]any{"data": []any{
		map[string]any{"cwd": "/project", "skills": items},
		map[string]any{"cwd": "/other-project", "skills": []any{fixtureSkill("foreign", "/foreign/SKILL.md", "repo")}},
	}}
	skills := codexSkillCatalog(response, "/project")
	if len(skills) != 3 {
		t.Fatal(skills)
	}
	seen := map[string]bool{"goal": true, "plan": true, "review": true}
	for _, skill := range skills {
		if seen[skill.command.Name] || !strings.HasPrefix(skill.command.Name, "skill:"+skill.name+":") || chatmodel.ParseSlashCommand("/"+skill.command.Name) == nil {
			t.Fatal("ambiguous or unselectable skill", skill)
		}
		seen[skill.command.Name] = true
	}
	items[0], items[1] = items[1], items[0]
	if !reflect.DeepEqual(skills, codexSkillCatalog(response, "/project")) {
		t.Fatal("native response ordering changed skill tokens")
	}
}

func TestNativeCodexSkillInputUsesResolvedNameAndPath(t *testing.T) {
	for _, arguments := range []string{"", "Investigate tests\nthen explain"} {
		t.Run(arguments, func(t *testing.T) {
			request := newAgentFixture(t, chatmodel.BackendCodex)
			writeFixtureSkills(t, request, []any{fixtureSkill("inspect", "/skills/inspect/SKILL.md", "repo")})
			request.Prompt = "/inspect " + arguments
			runFixture(t, newTestRunner(t), request)
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] != "turn/start" {
					continue
				}
				value := entry["value"].(map[string]any)
				input := value["params"].(map[string]any)["input"].([]any)
				text := arguments
				if text == "" {
					text = "$inspect"
				}
				if len(input) != 2 || input[0].(map[string]any)["text"] != text || !reflect.DeepEqual(input[1], map[string]any{"type": "skill", "name": "inspect", "path": "/skills/inspect/SKILL.md"}) {
					t.Fatalf("incorrect native skill input: %#v", input)
				}
				return
			}
			t.Fatal("no native skill turn")
		})
	}
}

func TestNativeCodexSkillExecutionRechecksEnabledState(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	skill := fixtureSkill("inspect", "/skills/inspect/SKILL.md", "repo")
	writeFixtureSkills(t, request, []any{skill})
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{request.Backend: request.Binary})
	if _, err := runner.PermissionModes(t.Context(), request.Backend, request.WorkingDirectory); err != nil {
		t.Fatal(err)
	}
	skill["enabled"] = false
	writeFixtureSkills(t, request, []any{skill})
	request.Prompt = "/inspect work"
	if _, err := runner.Run(t.Context(), request, nil); err == nil || !strings.Contains(err.Error(), "no prompt fallback") {
		t.Fatal(err)
	}
	if fixtureCount(t, request, "turn/start") != 0 {
		t.Fatal("disabled skill was sent to the model")
	}
}

func TestNativeGoalObjectiveIsNotReinterpretedAsCommand(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.Prompt = "/goal /unknown-command is the subject to investigate"
	runFixture(t, newTestRunner(t), request)
	if fixtureCount(t, request, "turn/start") != 1 {
		t.Fatal("goal objective was interpreted as a second command")
	}
}

func TestNativeCodexForkDoesNotStartModelTurn(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	request.NativeSessionID = "native-session-123"
	runner := newTestRunner(t)
	id, err := runner.ForkNativeSession(context.Background(), request)
	if err != nil || id == "" || id == request.NativeSessionID {
		t.Fatalf("fork: %q %v", id, err)
	}
	if fixtureCount(t, request, "thread/fork") != 1 || fixtureCount(t, request, "turn/start") != 0 {
		t.Fatal("fork started a model turn")
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ForkNativeSession(context.Background(), request); err == nil {
		t.Fatal("closed runner accepted a fork")
	}
}

func TestClaudeNativeCommandsAndSkillsUseAdvertisedTransport(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	for _, prompt := range []string{"/context", "/compact focus on tests", "/model test-claude", "/tidy", "/project:check"} {
		request.Prompt = prompt
		result, _ := runFixture(t, runner, request)
		request.NativeSessionID = result.SessionID
		if strings.HasPrefix(prompt, "/model") && result.Model != "test-claude" {
			t.Fatal(result)
		}
	}
	if fixtureCount(t, request, "user") != 5 || fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("commands were not dispatched in the same session")
	}
}

func TestClaudePrimaryCommandsAndSkillsExecuteInTheNativeSession(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	for _, prompt := range []string{"/code-review main", "/loop 5m check tests", "/deep-research investigate streaming", "/tidy tests", "/project:check target"} {
		request.Prompt = prompt
		result, _ := runFixture(t, runner, request)
		request.NativeSessionID = result.SessionID
	}
	if fixtureCount(t, request, "user") != 5 || fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("commands and skills did not use the native session")
	}
}
func TestUnknownSlashCommandNeverFallsBackToModel(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "/unknown-command fix tests"
			_, err := newTestRunner(t).Run(context.Background(), request, nil)
			if err == nil || !strings.Contains(err.Error(), "no prompt fallback") {
				t.Fatal(err)
			}
			if fixtureCount(t, request, "user") != 0 || fixtureCount(t, request, "turn/start") != 0 {
				t.Fatal("unknown command reached model")
			}
		})
	}
}

func TestNativePlanModeSetsPermissionBeforeStartingTask(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Prompt = "/plan investigate tests"
	result, _ := runFixture(t, newTestRunner(t), request)
	if result.PermissionMode != "plan" {
		t.Fatal(result)
	}
	for _, entry := range fixtureLog(t, request) {
		if entry["method"] == "user" && entry["permission_mode"] != "plan" {
			t.Fatal("plan permission not applied")
		}
	}
}
