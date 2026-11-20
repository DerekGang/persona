package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/promptcontent"
)

// taskInputContext resolves a draft through the existing task execution contract, without saving or preparing files.
func (s *Service) taskInputContext(ctx context.Context, raw map[string]json.RawMessage) (Task, TaskExecution, error) {
	t := Task{ID: "__input_draft"}
	var id string
	if v, ok := raw["id"]; ok {
		if err := json.Unmarshal(v, &id); err != nil {
			return t, TaskExecution{}, err
		}
	}
	if id != "" {
		sc, err := s.lockedSchedule(ctx)
		if err != nil {
			return t, TaskExecution{}, err
		}
		i := sc.indexLocked(id)
		if i >= 0 {
			t = sc.tasks[i]
		}
		sc.mu.Unlock()
		if i < 0 {
			return t, TaskExecution{}, i18n.Err("assistant-schedule.input_context_unavailable")
		}
	}
	previous := t
	if v, ok := raw["session_id"]; ok {
		var sid string
		if err := json.Unmarshal(v, &sid); err != nil {
			return t, TaskExecution{}, err
		}
		// The edit route may detach the original chat, never attach an arbitrary one.
		if sid == "" {
			t.SessionID = ""
		}
	}
	for key, dst := range map[string]any{"push": &t.Push, "repeat": &t.Repeat, "fresh_each_run": &t.FreshEachRun} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return t, TaskExecution{}, err
			}
		}
	}
	isolateScheduledConversation(&t, s.mayBeMainNow(ctx))
	edit, err := s.prepareTaskExecutionEdit(ctx, previous, &t, raw)
	if err != nil {
		return t, TaskExecution{}, err
	}
	t = s.taskInputDestination(ctx, t)
	ex, err := s.taskInputExecution(ctx, t, edit.record)
	return t, ex, err
}

// A task that wants visible updates runs in its own conversation when its source is another task's quiet chat.
func (s *Service) taskInputDestination(ctx context.Context, t Task) Task {
	if t.continuesSource() && !t.pushesOnlyAsked() && s.quietConversation(ctx, t.SessionID) {
		t.SessionID = ""
	}
	return t
}

func (s *Service) taskInputExecution(ctx context.Context, t Task, record *modelRecord) (TaskExecution, error) {
	if t.continuesSource() {
		c, ok := s.session(ctx, t.MachineID, t.SessionID)
		d, online := s.device(ctx, t.MachineID)
		if !online || !d.Online || s.isWorkMachine(t.MachineID) || !ok || !c.Assistant || c.Cwd == "" || s.isGuest(ctx, t.MachineID, t.SessionID) {
			return TaskExecution{}, i18n.Err("assistant-schedule.input_context_unavailable")
		}
		if t.Space == SpaceHealth {
			var canonical struct {
				Cwd string `json:"cwd"`
			}
			if c.Space != SpaceHealth || s.call(ctx, t.MachineID, assistantwire.DeviceAssistantDirectory, map[string]any{"path": s.healthCwd(d)}, &canonical) != nil || canonical.Cwd != c.Cwd {
				return TaskExecution{}, i18n.Err("assistant-schedule.input_context_unavailable")
			}
		}
		params := map[string]any{}
		if err := s.sendModel(ctx, t.MachineID, t.SessionID, params, s.requestCtxLang(ctx)); err != nil {
			return TaskExecution{}, err
		}
		return *s.sourceTaskExecution(ctx, t, params), nil
	}
	base, err := s.taskModelBaseline(ctx, s.taskMachine(ctx, t))
	if err != nil {
		return TaskExecution{}, err
	}
	if record == nil {
		stored, err := s.modelRecord(ctx, "task."+t.ID)
		if err != nil {
			return TaskExecution{}, err
		}
		record = &stored
	}
	b := mergeModel(base, record.Binding)
	ex, err := s.taskExecution(ctx, t, true, b)
	if err == nil && ex.Cwd == "" {
		if d, ok := s.device(ctx, ex.MachineID); ok {
			ex.Cwd = s.assistantCwd(d)
			if t.Space == SpaceHealth {
				ex.Cwd = s.healthCwd(d)
			}
		}
		var result struct {
			Cwd string `json:"cwd"`
		}
		if err = s.call(ctx, ex.MachineID, assistantwire.DeviceAssistantDirectory, map[string]any{"path": ex.Cwd}, &result); err == nil {
			if result.Cwd == "" {
				return ex, i18n.Err("assistant-schedule.input_context_unavailable")
			}
			ex.Cwd = result.Cwd
		}
	}
	return ex, err
}

func (s *Service) taskInputParams(ctx context.Context, t Task, ex TaskExecution, text string) map[string]any {
	params := map[string]any{"cwd": ex.Cwd, "launcher": ex.Launcher, "text": text, "task_execution": true, "machine_id": ex.MachineID, "skill_owner": s.skillOwner()}
	if t.continuesSource() {
		params["session_id"] = t.SessionID // the node reads the saved profile
	} else {
		s.withNewConversationProfile(ctx, ex.MachineID, params)
	}
	return params
}

func (s *Service) validateTaskInput(ctx context.Context, t Task, record *modelRecord) error {
	skills, err := assistantskills.References(t.Prompt)
	if err != nil {
		return skillReferenceError()
	}
	files, err := promptcontent.Files(t.Prompt)
	if err != nil {
		return i18n.Err("assistant-schedule.selected_file_unavailable")
	}
	if len(skills) == 0 && len(files) == 0 {
		return nil
	}
	t = s.taskInputDestination(ctx, t)
	ex, err := s.taskInputExecution(ctx, t, record)
	if err != nil {
		return err
	}
	var ack struct {
		Valid bool `json:"valid"`
	}
	if err := s.call(ctx, ex.MachineID, "assistant.content", s.taskInputParams(ctx, t, ex, t.Prompt), &ack); err != nil {
		return err
	}
	if !ack.Valid {
		return i18n.Err("assistant-schedule.input_context_unavailable")
	}
	return nil
}

func (s *Service) taskInputRoutes() {
	s.handle("POST /api/v1/assistant/tasks/input-context", func(w http.ResponseWriter, r *http.Request) {
		raw := map[string]json.RawMessage{}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if err := decoder.Decode(&raw); err != nil {
			apierr.Write(w, 400, err)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			apierr.Write(w, 400, errors.New("invalid request"))
			return
		}
		task, ex, err := s.taskInputContext(r.Context(), raw)
		if err != nil {
			apierr.Call(w, err)
			return
		}
		result := map[string]any{"execution": ex}
		var catalog bool
		_ = json.Unmarshal(raw["catalog"], &catalog)
		if catalog {
			params := s.taskInputParams(r.Context(), task, ex, "")
			sid := ""
			if task.continuesSource() {
				sid = task.SessionID
			}
			c, e := s.skillCatalogFor(r.Context(), ex.MachineID, sid, params)
			if e != nil {
				result["skill_error"] = e.Error()
			} else {
				result["catalog"] = c
			}
		}
		apierr.WriteJSON(w, result)
	})
}

// Keep the original envelope before the schedule header, so the shared selection parsers see it.
func taskPromptBody(prompt string) (string, string) {
	prefix, body := promptcontent.Split(prompt)
	if prefix == "" {
		return "", prompt
	}
	return strings.TrimSpace(prefix), strings.TrimLeft(body, "\r\n")
}
