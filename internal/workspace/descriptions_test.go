package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestDescriptionsManualEditsSurviveDiscoveryAndProjectSaves(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "descriptions")
	p := testProject(t, s, u)
	testAgent(t, s)
	if err := s.SaveDescriptions(DescriptionUpdate{DescriptionTarget: DescriptionTarget{"project", p.ID}, Items: []DescriptionEdit{{ID: "project", Text: "User-maintained project purpose"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDescriptions(DescriptionUpdate{DescriptionTarget: DescriptionTarget{"agent", "codex"}, Items: []DescriptionEdit{{ID: "agent", Text: "Local coding agent"}, {ID: "model:test-model", Text: "Use for repository work"}}}); err != nil {
		t.Fatal(err)
	}
	agents, _ := s.Agents()
	if err := s.saveAgent(agents[0]); err != nil {
		t.Fatal(err)
	}
	// A fresh provider catalog must not erase maintained descriptions.
	agents[0].Summary = nil
	agents[0].Catalog = fixtureCatalog()
	if err := s.saveAgent(agents[0]); err != nil {
		t.Fatal(err)
	}
	agents, _ = s.Agents()
	if agents[0].Summary.Text != "Local coding agent" || agents[0].Catalog.Models[0].Summary.Text != "Use for repository work" {
		t.Fatal("discovery replaced maintained descriptions")
	}
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	projects, _ := s.UserProjects(u.ID)
	if len(projects) != 1 || projects[0].Summary.Text != "User-maintained project purpose" {
		t.Fatal("project save or public projection lost the description")
	}
	data, _ := json.Marshal(agents)
	if !strings.Contains(string(data), "Use for repository work") || strings.Contains(string(data), `"executable"`) {
		t.Fatalf("public catalog has wrong projection: %s", data)
	}
	// Persisted descriptions are available after a new Store is opened.
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, _ := reopened.Agents()
	if restored[0].Summary.Text != "Local coding agent" {
		t.Fatal("description was not persisted")
	}
}

func TestDescriptionsConflictIsAtomicAndEmptyValuesKeepVersions(t *testing.T) {
	s := testStore(t)
	testAgent(t, s)
	target := DescriptionTarget{"agent", "codex"}
	if err := s.SaveDescriptions(DescriptionUpdate{target, []DescriptionEdit{{ID: "agent", Text: "First"}}}); err != nil {
		t.Fatal(err)
	}
	agents, _ := s.Agents()
	version := agents[0].Summary.Version
	if err := s.SaveDescriptions(DescriptionUpdate{target, []DescriptionEdit{{ID: "agent", ExpectedVersion: version, Text: ""}}}); err != nil {
		t.Fatal(err)
	}
	// Even clearing a description must reject a late generation based on First.
	err := s.SaveDescriptions(DescriptionUpdate{target, []DescriptionEdit{{ID: "model:test-model", Text: "Should roll back"}, {ID: "agent", Text: "Stale", ExpectedVersion: version}}})
	if err == nil {
		t.Fatal("stale update overwrote the cleared description")
	}
	agents, _ = s.Agents()
	if agents[0].Summary.Text != "" || agents[0].Summary.Version == version || agents[0].Catalog.Models[0].Summary != nil {
		t.Fatal("failed batch was not atomic")
	}
	for _, items := range [][]DescriptionEdit{
		{{ID: "model:unknown", Text: "Unknown model"}},
		{{ID: "agent", Text: "One"}, {ID: "agent", Text: "Two"}},
		{{ID: "agent", Text: strings.Repeat("x", 8193)}},
	} {
		if err := s.SaveDescriptions(DescriptionUpdate{target, items}); err == nil {
			t.Fatal("invalid description accepted")
		}
	}
}

func TestDescriptionPromptsHaveSeparateDefaultsAndPersistCustomizations(t *testing.T) {
	s := testStore(t)
	prompts, err := s.DescriptionPrompts()
	if err != nil || prompts["project"].Value == prompts["agent"].Value || prompts["project"].Value == "" {
		t.Fatal("missing separate defaults", err)
	}
	if err := s.SaveDescriptionPrompt("agent", "Custom system prompt"); err != nil {
		t.Fatal(err)
	}
	prompts, _ = s.DescriptionPrompts()
	if prompts["agent"].Value != "Custom system prompt" || prompts["project"].Value != projectSummaryPrompt {
		t.Fatal("prompt customization crossed target types")
	}
	if err := s.SaveDescriptionPrompt("agent", ""); err != nil {
		t.Fatal(err)
	}
	prompts, _ = s.DescriptionPrompts()
	if prompts["agent"].Value != agentSummaryPrompt {
		t.Fatal("reset did not restore built-in prompt")
	}
	if err := s.SaveDescriptionPrompt("model", "unexpected"); err == nil {
		t.Fatal("separate model prompt was accepted")
	}
}

func TestDescriptionProjectEvidenceIsBoundedAndExcludesExternalFiles(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s)
	if err := os.WriteFile(filepath.Join(p.Path, "README.md"), []byte("Order processing project"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private external contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Path, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Path, ".env"), []byte("SECRET=private"), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, items, err := s.descriptionContext(DescriptionTarget{"project", p.ID})
	if err != nil || len(items) != 1 || !strings.Contains(string(evidence), "Order processing") {
		t.Fatal(string(evidence), err)
	}
	for _, private := range []string{p.Path, "private external contents", "SECRET=private", `"members"`} {
		if strings.Contains(string(evidence), private) {
			t.Fatal("private evidence included", private)
		}
	}
}

type descriptionRunner struct {
	fakeRunner
	beforeReturn func(agent.AgentRequest)
	closed       bool
}

func (r *descriptionRunner) Run(ctx context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	if r.beforeReturn != nil {
		r.beforeReturn(request)
	}
	return r.fakeRunner.Run(ctx, request, emit)
}
func (r *descriptionRunner) Close() error { r.closed = true; return nil }

func TestDescriptionGenerationReturnsEditableAgentAndModelsWithChosenSettings(t *testing.T) {
	s := testStore(t)
	testAgent(t, s)
	runner := &descriptionRunner{fakeRunner: fakeRunner{text: `{"summaries":[{"id":"agent","text":"Coding agent"},{"id":"model:test-model","text":"Main model"},{"id":"model:other-model","text":"Alternate model"},{"id":"model:fixed-model","text":"Fixed reasoning"}]}`}}
	request := DescriptionRequest{DescriptionTarget: DescriptionTarget{"agent", "codex"}, Backend: chatmodel.BackendCodex, Model: "test-model", Effort: "high", Prompt: "Custom system instructions", Language: "zh-CN"}
	result, err := s.generateDescriptions(context.Background(), request, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 4 || result.Items[1].ID != "model:test-model" || result.Items[1].Backend != chatmodel.BackendCodex {
		t.Fatalf("wrong result %#v", result)
	}
	sent := runner.requests[0]
	if !sent.SummaryOnly || sent.NativeSessionID != "" || sent.Model != "test-model" || sent.ReasoningEffort != "high" || !strings.HasPrefix(sent.SystemPrompt, request.Prompt) || !strings.Contains(sent.Prompt, `"language":"zh-CN"`) {
		t.Fatalf("selection or prompt lost: %#v", sent)
	}
	if _, err := os.Stat(sent.WorkingDirectory); !os.IsNotExist(err) || !runner.closed {
		t.Fatal("owned runner/directory not cleaned up")
	}
	agents, _ := s.Agents()
	if agents[0].Summary != nil {
		t.Fatal("generation saved before the editable draft was accepted")
	}
	result.Items[1].Text = "User revised the generated model description"
	result.Items[1].Backend, result.Items[1].Model = "", ""
	if err := s.SaveDescriptions(DescriptionUpdate{request.DescriptionTarget, result.Items}); err != nil {
		t.Fatal(err)
	}
	agents, _ = s.Agents()
	if agents[0].Catalog.Models[0].Summary.Text != result.Items[1].Text {
		t.Fatal("manual revision lost")
	}
}

func TestDescriptionGenerationRejectsInvalidResultsAndStaleSources(t *testing.T) {
	for _, text := range []string{`not json`, `{"summaries":[]}`, `{"summaries":[{"id":"wrong","text":"Unexpected"}]}`, `{"summaries":[{"id":"project","text":""}]}`} {
		t.Run(text, func(t *testing.T) {
			s := testStore(t)
			testAgent(t, s)
			p := testProject(t, s)
			_, err := s.generateDescriptions(context.Background(), DescriptionRequest{DescriptionTarget: DescriptionTarget{"project", p.ID}, Backend: chatmodel.BackendCodex}, &descriptionRunner{fakeRunner: fakeRunner{text: text}})
			if err == nil {
				t.Fatal("invalid agent output accepted")
			}
		})
	}
	s := testStore(t)
	testAgent(t, s)
	p := testProject(t, s)
	request := DescriptionRequest{DescriptionTarget: DescriptionTarget{"project", p.ID}, Backend: chatmodel.BackendCodex}
	runner := &descriptionRunner{fakeRunner: fakeRunner{text: `{"summaries":[{"id":"project","text":"Stale description"}]}`}, beforeReturn: func(agent.AgentRequest) {
		if err := os.WriteFile(filepath.Join(p.Path, "README.md"), []byte("Changed during generation"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := s.generateDescriptions(context.Background(), request, runner); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatal("stale evidence was accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.generateDescriptions(ctx, request, &descriptionRunner{fakeRunner: fakeRunner{text: "unused"}}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not preserved", err)
	}
}

func TestDescriptionServiceCancelsAndReapsNativeWork(t *testing.T) {
	for _, action := range []string{"cancel", "stop", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			fixture := newAgentFixture(t, chatmodel.BackendCodex)
			if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendCodex, Enabled: true, Installed: true, Executable: fixture.Binary, Catalog: fixtureCatalog()}); err != nil {
				t.Fatal(err)
			}
			p := testProject(t, s)
			if err := os.WriteFile(filepath.Join(p.Path, "README.md"), []byte("steer-wait"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			service := &Service{Store: s, ctx: ctx}
			t.Cleanup(func() { cancel(); service.workers.Wait() })
			request := DescriptionRequest{ID: chatmodel.NewID(), DescriptionTarget: DescriptionTarget{"project", p.ID}, Backend: chatmodel.BackendCodex}
			done := make(chan error, 1)
			go func() { _, err := service.SummarizeDescriptions(ctx, request); done <- err }()
			deadline := time.After(8 * time.Second)
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				body, _ := os.ReadFile(filepath.Join(fixture.WorkingDirectory, "fixture-log.jsonl"))
				if strings.Contains(string(body), `"method":"turn/start"`) {
					break
				}
				select {
				case err := <-done:
					t.Fatal("summary exited before cancellation", err)
				case <-deadline:
					t.Fatal("summary did not start")
				case <-ticker.C:
				}
			}
			switch action {
			case "cancel":
				service.CancelDescriptionSummary(request.ID)
			case "stop":
				if err := service.Stop(); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("summary did not preserve cancellation", err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("summary did not reap its native process")
			}
			service.workers.Wait()
			service.mu.Lock()
			remaining := len(service.descriptionCalls)
			service.mu.Unlock()
			if remaining != 0 {
				t.Fatal("canceled summary remained registered")
			}
		})
	}
}
