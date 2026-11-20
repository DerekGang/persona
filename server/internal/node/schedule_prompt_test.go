package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The in-process hub supplies the single test machine and routes device calls to
// the real node handler. No model or hub service is needed by the Claude fixture.
type schedulePromptHub struct {
	node *Node
}

func (h schedulePromptHub) Hello(context.Context) (assistant.Hello, error) {
	id := h.node.machineID
	return assistant.Hello{Assistant: assistant.Placement{MachineID: id, Machines: []string{id}, Launcher: "claude"}}, nil
}

func (h schedulePromptHub) DeviceCall(ctx context.Context, p assistant.DeviceCall) (json.RawMessage, error) {
	if p.Device == assistant.DeviceAll && p.Method == assistant.DeviceDevicesList {
		d := assistant.Device{ID: h.node.machineID, Name: "Fixture", Home: h.node.home, DataDir: h.node.dir,
			Online: true, Reachable: true, ModelConfiguration: true}
		return json.Marshal(map[string]any{"devices": []assistant.Device{d}})
	}
	if p.Device != h.node.machineID {
		return nil, fmt.Errorf("test machine does not exist: %s", p.Device)
	}
	// The hub normally answers this from the node's session inventory.
	if p.Method == assistant.DeviceSessionGet {
		var req struct {
			ID string `json:"session_id"`
		}
		if err := json.Unmarshal(p.Params, &req); err != nil {
			return nil, err
		}
		for _, session := range h.node.sessionList(ctx, true) {
			if session.SessionID == req.ID {
				session.Assistant = h.node.isAssistantSession(req.ID)
				return json.Marshal(map[string]any{"ok": true, "session": session})
			}
		}
		return json.RawMessage(`{"ok":false}`), nil
	}
	result, err := h.node.handle(ctx, p.Method, p.Params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (schedulePromptHub) CredentialRevoked(context.Context, string) (bool, error) { return false, nil }
func (schedulePromptHub) AppCall(context.Context, assistant.AppCall) (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}
func (schedulePromptHub) NotifyRequest(context.Context, assistant.NotifyRequest) (assistant.NotifyResult, error) {
	return assistant.NotifyResult{}, nil
}
func (schedulePromptHub) Broadcast(context.Context, assistant.Broadcast) {}
func (schedulePromptHub) Snapshot(context.Context, assistant.Snapshot) (assistant.SnapshotResult, error) {
	return assistant.SnapshotResult{}, nil
}
func (schedulePromptHub) InboxChanged(context.Context, assistantwire.InboxChanged) {}
func (schedulePromptHub) Tell(string, any) error                                   { return nil }

func TestScheduledPromptDeliveryThroughNode(t *testing.T) {
	f := cliTestBash(t)
	f.n.machineID = "fixture"
	cwd := t.TempDir()
	file := filepath.Join(cwd, "notes.txt")
	selection := fmt.Sprintf(`<persona-context kind="file" path="notes.txt" machine="%s" cwd="%s" launcher="claude">Notes</persona-context>`, f.n.machineID, cwd)
	const notes = "Explain this example from our documentation."
	literal := selection + "\n\n" + notes
	service := assistant.New(assistant.Options{Hub: schedulePromptHub{f.n}})

	request := func(method, route string, body any, result any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, route, bytes.NewReader(raw)).WithContext(t.Context())
		service.ServeHTTP(w, r)
		if err := json.Unmarshal(w.Body.Bytes(), result); w.Code != http.StatusOK || err != nil {
			t.Fatalf("%s %s: %d %s (%v)", method, route, w.Code, w.Body, err)
		}
	}
	create := func(field, prompt string) string {
		t.Helper()
		var res struct{ Task assistant.Task }
		request(http.MethodPost, "/api/v1/assistant/tasks", map[string]any{
			"title": "Review", "repeat": "daily", "time": "09:00", "enabled": true, "cwd": cwd, field: prompt,
		}, &res)
		if res.Task.ID == "" || res.Task.Prompt != prompt || res.Task.PromptSelections != (field == "structured_prompt") {
			t.Fatalf("saved task lost prompt provenance: %+v", res.Task)
		}
		return res.Task.ID
	}
	run := func(taskID string) string {
		t.Helper()
		var res struct {
			SessionID string `json:"session_id"`
		}
		request(http.MethodPost, "/api/v1/assistant/tasks/"+taskID+"/run", nil, &res)
		if res.SessionID == "" {
			t.Fatal("task did not run")
		}
		f.idle(t, res.SessionID)
		return res.SessionID
	}
	input := func(want int) string {
		t.Helper()
		in := emptyStartUserInputs(t, f.file("stdin.jsonl"))
		if len(in) != want {
			t.Fatalf("got %d turns, want %d", len(in), want)
		}
		var text strings.Builder
		for _, content := range in[want-1].Message.Content {
			text.WriteString(content.Text)
		}
		return text.String()
	}

	task := create("prompt", literal)
	sid := run(task)
	if text := input(1); !strings.Contains(text, literal) || strings.Contains(text, "file-instructions") {
		t.Fatalf("scheduled start lost the literal example or added file instructions: %q", text)
	}
	if resumed := run(task); resumed != sid {
		t.Fatalf("scheduled resume started a new conversation: %q, want %q", resumed, sid)
	}
	if text := input(2); !strings.Contains(text, literal) || strings.Contains(text, "file-instructions") {
		t.Fatalf("scheduled resume lost the literal example or added file instructions: %q", text)
	}

	followupPrompt := selection + "\n\nExplain the file example in a follow-up note."
	args, err := json.Marshal(map[string]string{"at": "30m", "title": "Review example", "prompt": followupPrompt})
	if err != nil {
		t.Fatal(err)
	}
	if result := service.Tool(t.Context(), assistant.ToolCall{
		Name: "schedule_followup", Arguments: args, MachineID: f.n.machineID, SessionID: sid,
	}); result.IsError {
		t.Fatalf("schedule follow-up: %+v", result)
	}
	var list struct{ Followups []assistant.Task }
	request(http.MethodGet, "/api/v1/assistant/followups", nil, &list)
	if len(list.Followups) != 1 || list.Followups[0].Prompt != followupPrompt || list.Followups[0].PromptSelections {
		t.Fatalf("tool did not save a literal follow-up: %+v", list)
	}
	if continued := run(list.Followups[0].ID); continued != sid {
		t.Fatalf("follow-up started a new conversation: %q, want %q", continued, sid)
	}
	if text := input(3); !strings.Contains(text, followupPrompt) || strings.Contains(text, "file-instructions") {
		t.Fatalf("follow-up lost the literal example or added file instructions: %q", text)
	}

	// The documentation example needed no file. An approved selection
	// uses the fixture on disk and receives the corresponding instructions.
	writeFile(t, file, "Synthetic meeting notes")
	selectedPrompt := selection + "\n\nRead the selected notes."
	run(create("structured_prompt", selectedPrompt))
	if text := input(4); !strings.Contains(text, "file-instructions") || !strings.Contains(text, resolved(t, file)) {
		t.Fatalf("the selected task did not deliver file instructions: %q", text)
	}

	for _, tc := range []struct {
		name    string
		options map[string]any
	}{
		{"ordinary chat", nil},
		{"guest", map[string]any{"guest": map[string]any{"tools": []string{"lark_agent_reply"}}}},
		{"public", map[string]any{"public": true}},
		{"Inbox", map[string]any{"origin": originInbox, "origin_known": true}},
	} {
		t.Run(tc.name+" refuses a standalone file selection", func(t *testing.T) {
			params := map[string]any{"cwd": cwd, "launcher": "claude", "text": selectedPrompt}
			for key, value := range tc.options {
				params[key] = value
			}
			_, err := nodeCall(t, f.n, "assistant.send", params)
			var refusal *rpc.Error
			if !errors.As(err, &refusal) || refusal.Code != "file_unavailable" {
				t.Fatalf("file selection was not refused: %v", err)
			}
			input(4)
		})
	}
}

func TestScheduledRuntimeSourceRejectsFileSelection(t *testing.T) {
	n, hub := runtimeNode(t)
	n.machineID = "runtime-fixture"
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	cwd := filepath.Join(n.home, ".persona", "assistant")
	const id = "session-a1"
	host.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{"session_id": id, "cwd": cwd, "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	service := assistant.New(assistant.Options{Hub: schedulePromptHub{n}})
	source, err := service.Start(t.Context(), assistant.StartParams{Launcher: "dsh", MachineID: n.machineID,
		Cwd: cwd, Text: "Review the notes later", Untitled: true})
	if err != nil || source.SessionID != id {
		t.Fatalf("start runtime source: %+v (%v)", source, err)
	}
	writeFile(t, filepath.Join(cwd, "notes.txt"), "Synthetic notes")

	uuid := newUUID()
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{
		{"session_id": id, "cwd": cwd, "started_by_persona": true, "status": "idle"},
		{"session_id": uuid, "cwd": cwd, "started_by_persona": true, "status": "idle"},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both runtime conversations listed", func() bool {
		_, _, first := n.runtimes.rowOf(id)
		_, _, second := n.runtimes.rowOf(uuid)
		return first && second
	})

	const refusalKey = "assistant-schedule.file_runtime_unsupported"
	want := i18n.T(i18n.Default, refusalKey)
	selection := func(launcher string) string {
		return fmt.Sprintf(`<persona-context kind="file" path="notes.txt" machine="%s" cwd="%s" launcher="%s">Notes</persona-context>`, n.machineID, cwd, launcher)
	}
	for _, sid := range []string{id, uuid} {
		_, err := nodeCall(t, n, "assistant.send", map[string]any{
			"session_id": sid, "cwd": cwd, "text": selection("dsh"),
			"task_execution": true, "task_input": true, "resume_only": true,
		})
		if refusalCode(err) != "file_unavailable" || !strings.Contains(err.Error(), want) {
			t.Errorf("selected send on a runtime-owned conversation: %v", err)
		}
		_, err = nodeCall(t, n, "assistant.content", map[string]any{
			"session_id": sid, "cwd": cwd, "launcher": "claude", "text": selection("claude"), "task_execution": true,
		})
		if refusalCode(err) != "file_unavailable" || !strings.Contains(err.Error(), want) {
			t.Errorf("content approval on a runtime-owned conversation: %v", err)
		}
	}

	args := json.RawMessage(`{"at":"30m","title":"Review","prompt":"Review the notes"}`)
	if result := service.Tool(t.Context(), assistant.ToolCall{
		Name: "schedule_followup", Arguments: args, MachineID: n.machineID, SessionID: id,
	}); result.IsError {
		t.Fatalf("could not save an ordinary follow-up: %+v", result)
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload)).WithContext(t.Context())
		req.Header.Set("Accept-Language", string(i18n.Default))
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		return w
	}
	getTask := func() assistant.Task {
		t.Helper()
		w := request(http.MethodGet, "/api/v1/assistant/followups", nil)
		var list struct{ Followups []assistant.Task }
		if err := json.Unmarshal(w.Body.Bytes(), &list); w.Code != 200 || err != nil || len(list.Followups) != 1 {
			t.Fatalf("follow-ups: %d %s (%v)", w.Code, w.Body, err)
		}
		return list.Followups[0]
	}
	task := getTask()
	if task.SessionID != id {
		t.Fatalf("follow-up did not keep the runtime source: %+v", task)
	}

	preview := request(http.MethodPost, "/api/v1/assistant/tasks/input-context", map[string]any{"id": task.ID, "catalog": true})
	var result struct {
		TaskInput *bool  `json:"task_input"`
		Error     string `json:"input_error"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); preview.Code != 200 || err != nil || result.TaskInput == nil || *result.TaskInput || !strings.Contains(result.Error, want) {
		t.Errorf("preview did not explain the unavailable picker: %d %s (%v)", preview.Code, preview.Body, err)
	}
	path := "/api/v1/assistant/tasks/" + task.ID
	save := request(http.MethodPut, path, map[string]any{"structured_prompt": selection("dsh")})
	if save.Code == 200 || !strings.Contains(save.Body.String(), want) {
		t.Fatalf("save did not refuse with the runtime reason: %d %s", save.Code, save.Body)
	}
	if kept := getTask(); kept.Prompt != task.Prompt || kept.PromptSelections {
		t.Fatal("file selection modified the saved task")
	}

	before := len(host.got("session.send"))
	run := request(http.MethodPost, path+"/run", nil)
	sends := host.got("session.send")
	if run.Code != 200 || len(sends) != before+1 || !strings.Contains(string(sends[before]), task.Prompt) || len(host.got("session.start")) != 1 {
		t.Fatalf("plain follow-up did not continue its runtime: %d %s", run.Code, run.Body)
	}
}
