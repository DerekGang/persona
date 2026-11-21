package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// spawnWindowHub plays a node that runs the new session, and sends its first prompt, before session.start answers
// (#11999). While the start is in flight it reports the session list (with the child, and another Agent that just
// opened in the same workspace) through the manager's own sessions.changed handler, and, as the RPCs run in parallel,
// lets each of them list its tools and call task_spawn through the node's mcp.tools / mcp.call requests. Then the
// start answers, or fails with an unknown result.
type spawnWindowHub struct {
	Hub
	s      *Service
	f      *fakeHub
	armed  bool
	fail   error
	others []proto.Session
	child  string
	window map[string]spawnWindowSeen
	devs   *fleetDevs
}

// spawnWindowSeen is what a session got from the manager: whether task_spawn was listed, and the call's result.
type spawnWindowSeen struct {
	listed bool
	failed bool
	text   string
	broken string // the request did not reach the tool: a fixture fault, never a refusal
}

func (h *spawnWindowHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	raw, err := h.Hub.DeviceCall(ctx, p)
	if err != nil || p.Method != DeviceSessionStart || !h.armed {
		return raw, err
	}
	h.armed = false
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	child := res.Session
	child.MachineID, child.SpaceName, child.Cwd = p.Device, "app", "/code/app"
	child.Launcher, child.Agent, child.PermissionMode, child.Status = "codex", "codex", "plan", proto.StatusWorking
	h.child = child.SessionID
	h.f.mu.Lock()
	h.f.sessions[p.Device] = append(h.f.sessions[p.Device], append([]proto.Session{child}, h.others...)...)
	list := slices.Clone(h.f.sessions[p.Device])
	h.f.mu.Unlock()
	done := make(chan struct{})
	go func() { // another RPC, handled while this start has not answered
		defer close(done)
		h.syncDevs()
		h.s.SessionsChanged(context.Background(), DeviceSessions{MachineID: p.Device, Sessions: list})
		h.window = map[string]spawnWindowSeen{}
		for _, id := range append([]string{child.SessionID}, sessionIDs(h.others)...) {
			h.window[id] = spawnWindowNode(h, id, "Only inspect the second parser assertion; edit nothing and return its line.")
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("test fixture: the child's requests did not finish")
	}
	if h.fail != nil {
		return nil, h.fail
	}
	return raw, nil
}

func sessionIDs(list []proto.Session) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.SessionID)
	}
	return ids
}

// syncDevs mirrors the fake hub's rows into the devices, which the node requests' fleet gate reads. The devices are
// set once by the fixture and only their rows change here: background work started by sessions.changed reads them.
func (h *spawnWindowHub) syncDevs() {
	h.f.mu.Lock()
	list := slices.Clone(h.f.sessions["box"])
	h.f.mu.Unlock()
	h.devs.setSessions("box", list)
}

// spawnWindowNode asks as the node does for session: its tools (mcp.tools), then task_spawn (mcp.call).
func spawnWindowNode(h *spawnWindowHub, session, prompt string) spawnWindowSeen {
	ctx := context.Background()
	s := h.s
	h.syncDevs()
	var seen spawnWindowSeen
	req, _ := json.Marshal(map[string]any{"session_id": session})
	out, _, err := s.NodeRequest(ctx, "box", "mcp.tools", req)
	m, ok := out.(map[string]any)
	tools, listed := m["tools"].([]proto.Tool)
	if !ok || err != nil || !listed {
		seen.broken = fmt.Sprintf("mcp.tools: err=%v out=%v", err, out)
		return seen
	}
	seen.listed = toolListed(tools, "task_spawn")
	req, _ = json.Marshal(map[string]any{"session_id": session, "name": "task_spawn", "args": spawnPromptArgs(prompt)})
	out, handled, err := s.NodeRequest(ctx, "box", "mcp.call", req)
	r, ok := out.(proto.ToolResult)
	if err != nil || !handled || !ok || len(r.Content) == 0 {
		seen.broken = fmt.Sprintf("handled=%v err=%v out=%v", handled, err, out)
		return seen
	}
	seen.failed, seen.text = r.IsError, r.Content[0].Text
	return seen
}

// reached fails the test when a request never reached the tool, so a fixture fault cannot pass for a refusal.
func reached(t *testing.T, who string, seen spawnWindowSeen) spawnWindowSeen {
	t.Helper()
	if seen.broken != "" {
		t.Fatalf("%s: %s", who, seen.broken)
	}
	return seen
}

// A newly opened workspace Agent beside the child: linked by the same workspace while the start is in flight.
var spawnWindowSibling = proto.Session{MachineID: "box", SessionID: "sibling", SpaceName: "app", Cwd: "/code/app",
	Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}

func spawnWindowFixture(t *testing.T, fail error) (*Service, *fakeHub, Todo, *spawnWindowHub) {
	t.Helper()
	s, f, todo := spawnFixture(t, "codex", "plan")
	if _, err := s.linkTodoSpace(t.Context(), todo.ID, "box", "app"); err != nil {
		t.Fatal(err)
	}
	h := &spawnWindowHub{Hub: s.hub, s: s, f: f, armed: true, fail: fail, others: []proto.Session{spawnWindowSibling},
		devs: newFleetDevs()}
	h.devs.add("box", "Box")
	h.syncDevs()
	s.hub, s.devs = h, h.devs
	return s, f, todo, h
}

// The child of a task_spawn runs before its start answers. Until it is linked as spawned it neither sees nor can call
// task_spawn, however its workspace linked it; afterwards it is refused as a spawned child. An Agent that merely
// opened in the same workspace meanwhile is held back only while the start is in flight, then lists and uses
// task_spawn as before.
func TestTaskSpawnChildCannotSpawnBeforeItsStartAnswers(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, nil)
	before := len(f.starts)
	outer := s.mcpCall(t.Context(), "box", "s1", "task_spawn", spawnPromptArgs("Only inspect the first parser assertion; edit nothing and return its line."), false)
	if outer.IsError || h.child == "" {
		t.Fatalf("first layer refused or never started: %+v", outer)
	}
	if len(h.window) != 2 {
		t.Fatalf("fixture: the in-flight requests did not run: %+v", h.window)
	}
	child, sibling := reached(t, "child", h.window[h.child]), reached(t, "sibling", h.window["sibling"])
	t.Logf("in flight: child=%s %+v sibling %+v", h.child, child, sibling)
	if child.listed || !child.failed || !strings.Contains(child.text, "one supervised handoff layer") || !strings.Contains(child.text, "do not retry") {
		t.Errorf("child could spawn before its start answered: %+v", child)
	}
	if sibling.listed || !sibling.failed {
		t.Errorf("a session linked while the start was in flight passed for a first-layer Agent: %+v", sibling)
	}
	now := onlyTodo(s)
	link, ok := activeTodoAgent(now, "box", h.child)
	if !ok || link.Source != sourceSpawned || link.Via != "box/s1" {
		t.Fatalf("child is not linked as spawned by s1: %+v", now.Agents)
	}
	if side, ok := activeTodoAgent(now, "box", "sibling"); !ok || side.Source != "space" {
		t.Fatalf("workspace sibling lost its own link: %+v", now.Agents)
	}
	if len(f.starts) != before+1 || len(now.Agents) != 3 {
		t.Fatalf("starts_delta=%d want 1, links=%d want 3 (s1, child, sibling): %+v", len(f.starts)-before, len(now.Agents), now.Agents)
	}
	// The workspace sees the child again in the next session list: it does not link it back as space.
	h.syncDevs()
	h.f.mu.Lock()
	list := slices.Clone(h.f.sessions["box"])
	h.f.mu.Unlock()
	s.SessionsChanged(t.Context(), DeviceSessions{MachineID: "box", Sessions: list})
	if link, ok := activeTodoAgent(onlyTodo(s), "box", h.child); !ok || link.Source != sourceSpawned || link.Via != "box/s1" {
		t.Fatalf("the next session list relinked the child: %+v", onlyTodo(s).Agents)
	}
	after := reached(t, "child after", spawnWindowNode(h, h.child, "Only inspect the third assertion; edit nothing and return its line."))
	if after.listed || !after.failed || !strings.Contains(after.text, "already a spawned child") {
		t.Fatalf("linked child was not refused as spawned: %+v", after)
	}
	// The sibling is an ordinary workspace Agent once the child is linked: it lists task_spawn and starts its own child.
	again := reached(t, "sibling after", spawnWindowNode(h, "sibling", "Only inspect the lexer assertion; edit nothing and return its line."))
	if !again.listed || again.failed || len(f.starts) != before+2 {
		t.Fatalf("workspace Agent kept no task_spawn after the start answered: %+v starts_delta=%d", again, len(f.starts)-before)
	}
	for _, a := range onlyTodo(s).Agents {
		if a.Via == "box/"+h.child {
			t.Fatalf("a grandchild was linked: %+v", a)
		}
	}
	if _, ok := activeTodoAgent(onlyTodo(s), "box", "s1"); !ok {
		t.Fatal("parent lost its link")
	}
}

// When the start's result is unknown, the session the node may have started cannot be told from a workspace Agent
// linked meanwhile: both keep no task_spawn. Agents linked before or after keep it.
func TestTaskSpawnUnknownStartKeepsWindowSessionsFromSpawning(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, context.DeadlineExceeded)
	before := len(f.starts)
	outer := s.mcpCall(t.Context(), "box", "s1", "task_spawn", spawnPromptArgs("Only inspect the first parser assertion; edit nothing and return its line."), false)
	if !outer.IsError || h.child == "" {
		t.Fatalf("an unknown start result was reported as started: %+v", outer)
	}
	if len(h.window) != 2 {
		t.Fatalf("fixture: the in-flight requests did not run: %+v", h.window)
	}
	for _, id := range []string{h.child, "sibling"} {
		if seen := reached(t, id, h.window[id]); seen.listed || !seen.failed {
			t.Fatalf("%s could spawn while the start was in flight: %+v", id, seen)
		}
		if _, ok := activeTodoAgent(onlyTodo(s), "box", id); !ok {
			t.Fatalf("fixture: %s was not linked by its workspace: %+v", id, onlyTodo(s).Agents)
		}
		later := reached(t, id+" later", spawnWindowNode(h, id, "Only inspect the third assertion; edit nothing and return its line."))
		if later.listed || !later.failed || !strings.Contains(later.text, "one supervised handoff layer") {
			t.Fatalf("%s spawned after a start with an unknown result: %+v", id, later)
		}
	}
	// A workspace Agent linked after the start ended is not suspect.
	late := proto.Session{MachineID: "box", SessionID: "late", SpaceName: "app", Cwd: "/code/app", Launcher: "codex",
		Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	f.mu.Lock()
	f.sessions["box"] = append(f.sessions["box"], late)
	list := slices.Clone(f.sessions["box"])
	f.mu.Unlock()
	h.syncDevs()
	s.SessionsChanged(t.Context(), DeviceSessions{MachineID: "box", Sessions: list})
	if seen := reached(t, "late", spawnWindowNode(h, "late", "Only inspect the lexer assertion; edit nothing and return its line.")); !seen.listed || seen.failed {
		t.Fatalf("a workspace Agent linked after the start ended cannot spawn: %+v", seen)
	}
	if seen := reached(t, "s1", spawnWindowNode(h, "s1", "Only inspect the formatter assertion; edit nothing and return its line.")); !seen.listed || seen.failed {
		t.Fatalf("the original Agent cannot spawn after an unknown start: %+v", seen)
	}
	if len(f.starts) != before+3 {
		t.Fatalf("starts_delta=%d want 3 (the unknown start, late's child, s1's child)", len(f.starts)-before)
	}
}
