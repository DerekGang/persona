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
