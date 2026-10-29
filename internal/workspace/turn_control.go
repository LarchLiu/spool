package workspace

import (
	"context"
	"fmt"
	"net/http"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type turnControl struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

// The caller holds turnMu, so cancellation is registered before work can start.
func (a *App) registerTurn(parent context.Context, chatID, turnID string) (context.Context, *turnControl) {
	ctx, cancel := context.WithCancel(parent)
	control := &turnControl{id: turnID, cancel: cancel, done: make(chan struct{})}
	if a.activeTurns == nil {
		a.activeTurns = make(map[string]*turnControl)
	}
	a.activeTurns[chatID] = control
	a.runningTurns++
	return ctx, control
}

func (a *App) releaseTurn(chatID string, control *turnControl) {
	a.turnMu.Lock()
	if a.activeTurns[chatID] == control {
		delete(a.activeTurns, chatID)
	}
	a.runningTurns--
	a.turnMu.Unlock()
	control.cancel()
	close(control.done)
}

// stopTurn persists the terminal state before waiting for the provider
// process. A provider can take a while to reap a child process; the web UI
// must not remain stuck in the running state during that cleanup.
func (a *App) stopTurn(chat chatmodel.Session, turnID string, user string) (chatmodel.Session, error) {
	var current chatmodel.Turn
	a.liveMu.Lock()
	if live, ok := a.live[chat.ID]; ok && live.ID == turnID {
		current = live
	}
	a.liveMu.Unlock()

	result := agent.AgentResult{}
	if current.ID != "" {
		result = agent.AgentResult{
			Text:            current.Reply,
			Model:           current.Model,
			ReasoningEffort: current.ReasoningEffort,
			Activities:      current.Activities,
			Parts:           current.Parts,
		}
	}
	if err := a.store.finishTurn(chat.ID, turnID, result, context.Canceled, chatmodel.StatusStopped, user); err != nil {
		return chatmodel.Session{}, err
	}
	latest, ok := a.store.Get(chat.ID)
	if !ok {
		return chatmodel.Session{}, fmt.Errorf("chat disappeared while stopping")
	}

	// Keep complete partial output until the native session catches up, just
	// as for a normally completed turn.
	a.liveMu.Lock()
	if live, ok := a.live[chat.ID]; ok && live.ID == turnID {
		live.Status = chatmodel.StatusStopped
		if len(latest.Turns) > 0 {
			finished := latest.Turns[len(latest.Turns)-1]
			live.FinishedAt = finished.FinishedAt
			live.Error = finished.Error
			live.Actions = finished.Actions
		}
		base := latest.Transcript
		if hydrated, exists := a.liveBase[chat.ID]; exists {
			base = hydrated
		}
		latest.Transcript = runningTranscript(base, live)
		latest.Turns = append(append([]chatmodel.Turn(nil), a.liveTurns[chat.ID]...), live)
		start := max(0, min(live.PartStart, len(latest.Transcript.Parts)))
		live.PartStart = start
		live.InputEnd = max(start, min(live.InputEnd, len(latest.Transcript.Parts)))
		live.Parts = append([]chatmodel.Part(nil), latest.Transcript.Parts[start:]...)
		live.PartEnd = live.PartStart + len(live.Parts)
		if len(latest.Turns) > 0 {
			latest.Turns[len(latest.Turns)-1] = live
		}
		a.completed[chat.ID] = completedTurn{turn: live}
		delete(a.live, chat.ID)
		delete(a.liveBase, chat.ID)
		delete(a.liveTurns, chat.ID)
	}
	a.liveMu.Unlock()
	latest.NativeSessionID = ""
	return latest, nil
}

func (a *App) stopTurnResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, user string) {
	var input struct {
		TurnID string `json:"turn_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !chatmodel.ValidID(input.TurnID) {
		apiError(w, 400, "Invalid turn ID")
		return
	}
	a.turnMu.Lock()
	control := a.activeTurns[chat.ID]
	if control == nil || control.id != input.TurnID {
		control = nil
	}
	a.turnMu.Unlock()
	if control == nil {
		latest, ok := a.store.Get(chat.ID)
		// Repeating a stop is harmless, but a stale click must never stop a new turn.
		if ok && len(latest.Turns) > 0 {
			last := latest.Turns[len(latest.Turns)-1]
			if last.ID == input.TurnID && last.Status != chatmodel.StatusRunning {
				apiJSON(w, 200, historyWindow(a.snapshot(latest)))
				return
			}
			// The process may have disappeared while the server was still alive
			// (for example after a provider crash). Reconcile the persisted
			// running turn instead of leaving the chat permanently busy.
			if last.ID == input.TurnID && last.Status == chatmodel.StatusRunning {
				stopped, err := a.stopTurn(chat, input.TurnID, user)
				if err != nil {
					apiError(w, 500, "Could not stop this turn")
					return
				}
				apiJSON(w, 200, historyWindow(stopped))
				return
			}
		}
		apiError(w, 409, "This turn is no longer running")
		return
	}
	// Persist the stopped state immediately. Waiting for control.done here
	// made the stop request depend on provider process reaping and could leave
	// both the button and the chat spinner stuck indefinitely.
	latest, err := a.stopTurn(chat, input.TurnID, user)
	if err != nil {
		apiError(w, 500, "Could not stop this turn")
		return
	}
	control.cancel()
	// CLIRunner keeps one process per chat. Cancel it explicitly so a
	// follow-up turn can reserve the chat while the old process is being
	// reaped in the background.
	if stopper, ok := a.runner.(interface{ Stop(string) }); ok {
		stopper.Stop(chat.ID)
	}
	apiJSON(w, http.StatusOK, historyWindow(latest))
}
