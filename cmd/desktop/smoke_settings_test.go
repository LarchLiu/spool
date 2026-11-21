package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestSmokeSettingsCommandWaitsForAutosave(t *testing.T) {
	attempts, writes := 0, 0
	err := retrySmokeSettingsCommand(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return &mygo.EvalError{Message: "Working…"}
		}
		writes++
		return nil
	})
	if err != nil || attempts != 3 || writes != 1 {
		t.Fatalf("command did not resume once autosave finished: attempts=%d writes=%d err=%v", attempts, writes, err)
	}
}

func TestSmokeSettingsCommandPreservesOtherErrors(t *testing.T) {
	for _, want := range []error{errors.New("Working…"), &mygo.EvalError{Message: "invalid settings"}} {
		attempts := 0
		err := retrySmokeSettingsCommand(context.Background(), func() error {
			attempts++
			return want
		})
		if err != want || attempts != 1 {
			t.Fatalf("unexpected retry: attempts=%d err=%v want=%v", attempts, err, want)
		}
	}
}

func TestSmokeSettingsCommandHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retrySmokeSettingsCommand(ctx, func() error {
		t.Fatal("command ran after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	attempts := 0
	err = retrySmokeSettingsCommand(ctx, func() error {
		attempts++
		return &mygo.EvalError{Message: "Working…"}
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("busy command did not stop at its deadline: attempts=%d err=%v", attempts, err)
	}
}
