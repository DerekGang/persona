package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestTaskExecutionOriginEdits(t *testing.T) {
	for _, tc := range []struct {
		name, patch string
		want        int
		own         bool
	}{
		{"inherit computer only", `"execution_machine_id":""`, http.StatusOK, false},
		{"clear defaults", `"execution_machine_id":"","cwd":"","launcher":"","model":"","effort":""`, http.StatusOK, false},
		{"pin inherited computer", `"execution_machine_id":"server"`, http.StatusBadRequest, false},
		{"set directory", `"cwd":"/work/project"`, http.StatusBadRequest, false},
		{"set model", `"model":"test-model"`, http.StatusBadRequest, false},
		{"own chat can pin computer", `"execution_machine_id":"server"`, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newTestService(t, f)
			setTasks(s, Task{ID: "job", Title: "Check", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, MachineID: "server", SessionID: "origin"})
			sessionID := "origin"
			machine := ""
			if tc.own {
				sessionID, machine = "", "server"
			}
			body := fmt.Sprintf(`{"title":"Check","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true,"session_id":%q,%s}`, sessionID, tc.patch)
			r := request(s, "PUT", "/api/v1/assistant/tasks/job", body)
			if r.Code != tc.want {
				t.Fatalf("save: %d %s", r.Code, r.Body.String())
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(r.Body.String(), "execution_own_chat") && !strings.Contains(r.Body.String(), "原来的对话") {
				t.Fatalf("unrelated validation: %s", r.Body.String())
			}
			got, _ := taskByID(s, "job")
			if got.Title != "Check" || got.Prompt != "Check files" || got.Repeat != repeatInterval || got.EveryMinutes != 30 || !got.Enabled {
				t.Fatalf("execution edit changed task content or schedule: %+v", got)
			}
			if got.SessionID != sessionID || got.ExecutionMachineID != machine {
				t.Fatalf("execution edit changed the wrong context: %+v", got)
			}
		})
	}
}

func TestTaskExecutionOriginClearsHistoricalOverrides(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s, Task{ID: "job", Title: "Check", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, MachineID: "server", SessionID: "origin", ExecutionMachineID: "server", Cwd: "/work/project"})
	setModel(t, s, "task.job", ModelBinding{Provider: "launcher.claude", Model: "test-model"})
	body := `{"title":"Changed","prompt":"Check files","repeat":"interval","every_minutes":30,"enabled":true,"session_id":"origin"}`
	if r := request(s, "PUT", "/api/v1/assistant/tasks/job", body); r.Code != http.StatusBadRequest {
		t.Fatalf("historical overrides silently retained: %d %s", r.Code, r.Body.String())
	}
	body = strings.TrimSuffix(body, "}") + `,"execution_machine_id":"","cwd":"","launcher":"","model":"","effort":""}`
	// Clearing overrides to continue the source needs no live model or directory validation.
	f.devices = []Device{{ID: "server", Online: false}}
	if r := request(s, "PUT", "/api/v1/assistant/tasks/job", body); r.Code != http.StatusOK {
		t.Fatalf("clear historical overrides: %d %s", r.Code, r.Body.String())
	}
	task, _ := taskByID(s, "job")
	record, err := s.modelRecord(context.Background(), "task.job")
	if err != nil || task.SessionID != "origin" || task.Cwd != "" || task.ExecutionMachineID != "" || record.Binding != (ModelBinding{}) {
		t.Fatalf("did not restore inheritance: %+v %+v %v", task, record, err)
	}
}

func TestTaskSourceFallbackDeliveryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"gone", fmt.Errorf("conversation origin is gone"), true},
		{"offline before dispatch", fmt.Errorf("source: %w", apierr.ErrOffline), true},
		{"link closed", rpc.ErrClosed, false},
		{"reply lost", context.DeadlineExceeded, false},
		{"other refusal", errors.New("send failed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newTestService(t, f)
			s.runRetry = 10 * time.Millisecond
			var source, own atomic.Int32
			f.answerNode(DeviceAssistantSend, func(_ string, raw json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(raw, &p)
				if p.SessionID == "origin" {
					source.Add(1)
					return nil, tc.err
				}
				own.Add(1)
				return map[string]string{"session_id": "own"}, nil
			})
			setTasks(s, Task{ID: "job", Prompt: "Check files", Repeat: repeatOnce, Enabled: true, At: time.Now().Add(-time.Minute), MachineID: "server", SessionID: "origin"})
			got, err := runNow(t, s, "job")
			if tc.fallback {
				if err != nil || got.SessionID != "own" || own.Load() != 1 {
					t.Fatalf("safe fallback: %+v %v source=%d own=%d", got, err, source.Load(), own.Load())
				}
			} else {
				if err == nil || own.Load() != 0 {
					t.Fatalf("uncertain send replaced: %+v %v own=%d", got, err, own.Load())
				}
				if len(f.pushed()) == 0 {
					t.Fatal("uncertain delivery lost the failure notice")
				}
				// The delayed one-off retry must not duplicate an uncertain source send either.
				time.Sleep(100 * time.Millisecond)
				if source.Load() != 1 || own.Load() != 0 {
					t.Fatalf("uncertain source retried: source=%d own=%d", source.Load(), own.Load())
				}
			}
		})
	}
}

func TestTaskSourceGoneOnAnExistingMachine(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s, Task{ID: "job", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, MachineID: "server", SessionID: "gone"})
	got, err := runNow(t, s, "job")
	if err != nil || got.SessionID == "" || got.SessionID == "gone" {
		t.Fatalf("missing source did not fall back: %s %v", got.SessionID, err)
	}
	runs := waitRuns(t, s, 1)
	if runs[0].Conversation != "new" || runs[0].ConversationReason != "source_unavailable" {
		t.Fatalf("source fallback absent from history: %+v", runs[0])
	}
}

func TestTaskSourceModelPreparationFallsBackBeforeSending(t *testing.T) {
	s, f := pickFixture(t)
	setTasks(s, Task{ID: "job", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, MachineID: "server", SessionID: "main"})
	// This fixture supplies an authoritative launcher list: "gone" is refused before assistant.send.
	if err := s.setModelPick(context.Background(), "main", &modelPick{MachineID: "server", Launcher: "gone", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	got, err := runNow(t, s, "job")
	if err != nil || got.SessionID == "" || got.SessionID == "main" || f.sent() != 1 {
		t.Fatalf("model preparation fallback: %s %v sends=%d", got.SessionID, err, f.sent())
	}
	if runs := waitRuns(t, s, 1); runs[0].ConversationReason != "source_unavailable" {
		t.Fatalf("source model preparation was not exercised: %+v", runs[0])
	}
}

func TestTaskSourceRejectsExplicitExecutionBeforeSending(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s, Task{ID: "job", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true, MachineID: "server", SessionID: "origin", ExecutionMachineID: "server"})
	if _, err := runNow(t, s, "job"); err == nil || f.sent() != 0 {
		t.Fatalf("explicit source context ignored: %v sent=%d", err, f.sent())
	}
}

func TestTaskExecutionKnownMissingLauncherIsNotSubstituted(t *testing.T) {
	s, f := pickFixture(t)
	setTasks(s, Task{ID: "job", Prompt: "Check files", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true})
	setModel(t, s, "task.job", ModelBinding{Provider: "launcher.gone"})
	if _, err := runNow(t, s, "job"); err == nil || f.sent() != 0 {
		t.Fatalf("missing launcher was substituted: %v sent=%d", err, f.sent())
	}
}
