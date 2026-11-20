package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestTaskInputContextUsesDraftExecutionWithoutSaving(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	f.zshrc = map[string][]proto.Launcher{"remote": {{Name: "codex", Agent: "codex"}}}
	f.answerNode("assistant.directory", func(machine string, raw json.RawMessage) (any, error) {
		if machine != "remote" {
			t.Fatalf("directory checked on %s", machine)
		}
		return map[string]any{"cwd": "/work/project"}, nil
	})
	var catalog map[string]any
	f.answerNode("skills.list", func(machine string, raw json.RawMessage) (any, error) {
		if machine != "remote" {
			t.Fatalf("catalog on %s", machine)
		}
		_ = json.Unmarshal(raw, &catalog)
		return assistantskills.Catalog{Version: 1, Complete: true}, nil
	})
	s := newTestService(t, f)
	setTasks(s)
	r := request(s, "POST", "/api/v1/assistant/tasks/input-context", `{"launcher":"codex","model":"test-model","execution_machine_id":"remote","cwd":"/work/project","catalog":true}`)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"machine_id":"remote"`) || catalog["cwd"] != "/work/project" || catalog["launcher"] != "codex" {
		t.Fatalf("context: %d %s %+v", r.Code, r.Body.String(), catalog)
	}
	if len(s.tasksView(context.Background(), nil)) != 0 {
		t.Fatal("discovery saved a task")
	}
	b, _ := s.modelRecord(context.Background(), "task.__input_draft")
	if b.Revision != 0 {
		t.Fatal("discovery saved a model override")
	}
}

func TestTaskPromptKeepsSelectionsBeforeScheduleHeader(t *testing.T) {
	block := `<persona-context kind="skill" ref="selected">Review</persona-context>`
	got := taskPrompt(Task{Title: "Review", Prompt: block + "\n\nCheck files", Repeat: repeatInterval, EveryMinutes: 30}, time.Now(), time.Now(), time.UTC, false, false, false)
	refs, err := assistantskills.References(got)
	if err != nil || len(refs) != 1 || refs[0] != "selected" || !strings.Contains(got, "Check files") {
		t.Fatalf("lost selection: %s %v", got, err)
	}
}

func TestTaskInputSaveReopenAndBothRunPaths(t *testing.T) {
	f := newFakeHub().withMachine()
	f.sessions["remote"] = nil
	f.zshrc = map[string][]proto.Launcher{"remote": {{Name: "claude", Agent: "claude"}}}
	s := newTestService(t, f)
	setTasks(s)
	content := 0
	f.answerNode("assistant.content", func(machine string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		content++
		if machine != "remote" || p["cwd"] != "/work/project" || p["launcher"] != "claude" || p["skill_owner"] != s.skillOwner() {
			t.Fatalf("save context: %s %v", machine, p)
		}
		return map[string]any{"valid": true}, nil
	})
	f.answerNode("skills.list", func(machine string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if machine != "remote" || p["cwd"] != "/work/project" {
			t.Fatalf("run catalog: %s %v", machine, p)
		}
		return assistantskills.Catalog{Version: 1, Skills: []assistantskills.Entry{{ID: "review", Name: "Review"}}}, nil
	})
	prompt := `<persona-context kind="skill" ref="review">Review</persona-context>` + "\n" + `<persona-context kind="file" path="notes.txt" machine="remote" cwd="/work/project" launcher="claude" runtime="">Notes</persona-context>` + "\n\nReview files"
	body, _ := json.Marshal(map[string]any{"title": "Review", "prompt": prompt, "repeat": "interval", "every_minutes": 30, "enabled": true, "execution_machine_id": "remote", "cwd": "/work/project", "model": "test-model"})
	r := request(s, "POST", "/api/v1/assistant/tasks", string(body))
	if r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	var reply struct{ Task Task }
	_ = json.Unmarshal(r.Body.Bytes(), &reply)
	if reply.Task.Prompt != prompt || content != 1 {
		t.Fatalf("save lost references: %+v", reply)
	}
	restarted := newServiceOn(t, f, s.store())
	reopened := restarted.tasksView(context.Background(), nil)
	if len(reopened) != 1 || reopened[0].Prompt != prompt {
		t.Fatalf("reopen: %+v", reopened)
	}
	manual := request(s, "POST", "/api/v1/assistant/tasks/"+reply.Task.ID+"/run", "{}")
	if manual.Code != 200 {
		t.Fatal(manual.Body.String())
	}
	var first proto.Session
	err := json.Unmarshal(manual.Body.Bytes(), &first)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.lastSend(t); got["task_input"] != true || got["skill_owner"] != s.skillOwner() || !strings.Contains(got["text"].(string), `path="notes.txt"`) || got["cwd"] != "/work/project" || got["model"] != "test-model" {
		t.Fatalf("manual start lost selection or execution parameters: %v", got)
	}
	f.setSessions("remote", proto.Session{SessionID: first.SessionID, Assistant: true, Cwd: "/work/project", Launcher: "claude", Agent: "claude"})
	task, _ := taskByID(s, reply.Task.ID)
	if _, err := s.runTask(task, time.Now(), time.Now(), false, false); err != nil {
		t.Fatal(err)
	}
	if got := f.lastSend(t); got["task_input"] != true || got["skill_owner"] != s.skillOwner() || !strings.Contains(got["text"].(string), `ref="review"`) {
		t.Fatalf("resume lost refs: %v", got)
	}
	f.answerNode("assistant.content", func(string, json.RawMessage) (any, error) { return nil, errors.New("reference changed") })
	before, err := s.modelRecord(t.Context(), "task."+reply.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	edit := strings.NewReplacer(`"title":"Review"`, `"title":"Changed"`, `"model":"test-model"`, `"model":"new-model"`).Replace(string(body))
	r = request(s, "PUT", "/api/v1/assistant/tasks/"+reply.Task.ID, edit)
	if r.Code == 200 {
		t.Fatal("stale reference accepted")
	}
	kept, _ := taskByID(s, reply.Task.ID)
	after, err := s.modelRecord(t.Context(), "task."+reply.Task.ID)
	if err != nil || before != after || kept.Prompt != prompt || kept.Title != "Review" {
		t.Fatal("failed save changed task")
	}
}

func TestTaskInputDoesNotGrantUnattendedStarts(t *testing.T) {
	for _, kind := range []string{"file", "skill"} {
		t.Run(kind, func(t *testing.T) {
			task := Task{Prompt: `<persona-context kind="` + kind + `">Selection</persona-context> Read the notes`}
			if task.grantEligible() {
				t.Fatal("mutable reference admitted a clean-round start grant")
			}
		})
	}
}

func TestTaskInputOfflineRefusalIsAtomic(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setTasks(s, Task{ID: "offline", Title: "Plain", Prompt: "Notes", Repeat: repeatInterval, EveryMinutes: 30})
	f.devices = []Device{{ID: "server", Online: false}}
	prompt := `<persona-context kind="file" machine="server" path="notes" cwd="/work" launcher="claude">Notes</persona-context>`
	body, _ := json.Marshal(map[string]any{"title": "Changed", "prompt": prompt, "repeat": "interval", "every_minutes": 30})
	r := request(s, "PUT", "/api/v1/assistant/tasks/offline", string(body))
	task, _ := taskByID(s, "offline")
	if r.Code == 200 || task.Prompt != "Notes" || task.Title != "Plain" || f.sent() != 0 {
		t.Fatalf("offline edit: %d %+v", r.Code, task)
	}
}
