package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/conversationlog"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func logicalPair(t *testing.T, engine string) (*Service, *fakeHub, *db.DB, *db.LogicalConversation) {
	t.Helper()
	s, f, d, _ := historyFixture(t)
	ctx := t.Context()
	f.setSessions("server", proto.Session{SessionID: "side-chat", Assistant: true, Agent: "claude", Rev: 100}, proto.Session{SessionID: "target-chat", Assistant: true, Agent: engine, Rev: 100})
	if err := d.AdoptConversation(ctx, "server", "side-chat", "one", "claude", "h1"); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(logicalSwitchRequest{OperationID: "operation", ExpectedEpoch: 1, MachineID: "server", Launcher: engine, Model: "target-model", Effort: "medium"})
	if err := d.BeginConversationSwitch(ctx, "server", "side-chat", db.ConversationSwitch{OperationID: "operation", ExpectedEpoch: 1, SourceSegment: "one", Request: p}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutConversationHandoff(ctx, "server", "side-chat", "operation", json.RawMessage(`{"summary":"fake source summary","recent":[{"role":"user","text":"same conversation"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.PrepareConversationSegment(ctx, "server", "side-chat", "operation", conversationlog.Segment{ID: "two", MachineID: "server", SessionID: "target-chat", Engine: engine, SourceHistory: "h2", Launcher: engine, Model: "target-model", Effort: "medium"}, 6); err != nil {
		t.Fatal(err)
	}
	c, err := d.LogicalConversation(ctx, "server", "side-chat")
	if err != nil {
		t.Fatal(err)
	}
	return s, f, d, c
}
func activatePair(t *testing.T, d *db.DB) *db.LogicalConversation {
	t.Helper()
	ctx := t.Context()
	if err := d.ActivateConversationSegment(ctx, "server", "side-chat", "operation", 1); err != nil {
		t.Fatal(err)
	}
	c, err := d.LogicalConversation(ctx, "server", "side-chat")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLogicalHistoryProjectsDatabaseAcrossSegmentsAndFrozenTail(t *testing.T) {
	s, _, d, _ := logicalPair(t, "pi")
	ctx := t.Context()
	first := []proto.Item{{ID: "line1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "first"}, {ID: "line6#0", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "last"}, {ID: "line9#0", Seq: 9, Role: "assistant", Kind: proto.KindText, Text: "retired append"}}
	second := []proto.Item{{ID: "line1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "next"}, {ID: "line4#0", Seq: 4, Role: "assistant", Kind: proto.KindText, Text: "reply", Image: &proto.Image{Mime: "image/png", Data: "eA=="}}}
	for _, p := range []db.ConversationPage{{MachineID: "server", SessionID: "side-chat", History: "h1", Revision: 100, ThroughSeq: 9, Tail: true, Items: first}, {MachineID: "server", SessionID: "target-chat", History: "h2", Revision: 100, ThroughSeq: 4, Tail: true, Items: second}} {
		if err := d.PutConversationPage(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	c := activatePair(t, d)
	after := 0
	page, err := s.logicalHistoryPage(ctx, d, c, historyParams{After: &after, LazyImages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.Items[0].ID != "line1#0" || page.Items[2].ID != "segment:two:line1#0" || page.Items[2].Seq != 7 || intValue(page.ToSeq) != 10 || page.MoreAfter {
		t.Fatalf("projection: %+v", page)
	}
	for _, it := range page.Items {
		if it.Text == "retired append" {
			t.Fatal("retired tail leaked")
		}
	}
	raw, err := s.logicalFullItem(ctx, c, "session.image", historyParams{ItemID: "segment:two:line4#0"})
	if err != nil || !strings.Contains(string(raw), "eA==") {
		t.Fatalf("logical image: %s %v", raw, err)
	}
	before := 8
	page, err = s.logicalHistoryPage(ctx, d, c, historyParams{Before: &before, Turns: 2, FoldWork: true})
	if err != nil {
		t.Fatal(err)
	}
	if intValue(page.ToSeq) > 7 || len(page.Items) == 0 {
		t.Fatalf("cross-boundary backwards page: %+v", page)
	}
	// A new manager reconstructs exactly the same stable projection from Postgres.
	restarted := withSettings(t, s.hub.(*fakeHub), New(Options{Hub: s.hub, Store: func() Store { return d }}))
	raw, handled, err := restarted.logicalCall(ctx, "server", DeviceSessionItems, json.RawMessage(`{"session_id":"side-chat","after_seq":6}`))
	if !handled || err != nil || !strings.Contains(string(raw), "segment:two:line1#0") {
		t.Fatalf("restart: %s %v %v", raw, handled, err)
	}
	if _, handled, err := s.logicalCall(ctx, "server", DeviceSessionItems, json.RawMessage(`{"session_id":"target-chat"}`)); !handled || err == nil {
		t.Fatal("private target became public")
	}
}

func TestLogicalFirstInputPolicyAndForwardRollback(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(fmt.Sprint(refused), func(t *testing.T) {
			s, f, d, _ := logicalPair(t, "codex")
			ctx := t.Context()
			activatePair(t, d)
			setSettings(t, s, func(cfg *Settings) { cfg.Leaders = []string{"side-chat"} })
			sends := 0
			f.answerNode(DeviceAssistantSend, func(_ string, raw json.RawMessage) (any, error) {
				sends++
				var p map[string]any
				_ = json.Unmarshal(raw, &p)
				if p["session_id"] != "target-chat" || p["client_msg_id"] != "original-id" || p["supervision_instructions"] == "" || !strings.Contains(fmt.Sprint(p["text"]), "fake source summary") {
					t.Fatalf("first delivery lost logical role/context/identity: %s", raw)
				}
				if refused {
					return nil, &rpc.Error{Code: "assistant_supervision_runtime", Message: "fake policy refusal", Data: json.RawMessage(`{"delivered":"none"}`)}
				}
				return map[string]any{"session_id": "target-chat"}, nil
			})
			raw, handled, err := s.logicalCall(ctx, "server", DeviceAssistantSend, json.RawMessage(`{"session_id":"side-chat","client_msg_id":"original-id","text":"continue"}`))
			if err != nil || !handled || !strings.Contains(string(raw), `"queued":true`) {
				t.Fatalf("queue receipt: %s %v", raw, err)
			}
			c, _ := d.LogicalConversation(ctx, "server", "side-chat")
			err = s.drainLogicalInput(ctx, d, c)
			if refused && err == nil || !refused && err != nil {
				t.Fatalf("delivery result: %v", err)
			}
			c, _ = d.LogicalConversation(ctx, "server", "side-chat")
			a, ok := c.Active()
			if !ok || sends != 1 {
				t.Fatalf("delivery repeated: %+v sends %d", c, sends)
			}
			if refused {
				if c.Epoch != 3 || a.ID != "one" {
					t.Fatalf("rollback did not go forward: %+v", c)
				}
			} else {
				if c.Epoch != 2 || a.ID != "two" || !c.Segments[0].ClosePending {
					t.Fatalf("accepted source closure: %+v", c)
				}
			}
		})
	}
}

func TestLogicalRetiredAuthorityAndRealTimeProjection(t *testing.T) {
	s, f, d, _ := logicalPair(t, "agy")
	ctx := t.Context()
	c := activatePair(t, d)
	if _, _, _, err := s.logicalCaller(ctx, "server", "side-chat"); err == nil {
		t.Fatal("retired caller retained logical authority")
	}
	m, id, _, err := s.logicalCaller(ctx, "server", "target-chat")
	if err != nil || m != "server" || id != "side-chat" {
		t.Fatalf("active caller: %s/%s %v", m, id, err)
	}
	s.NodeEvent("server", "session.items", json.RawMessage(`{"session_id":"target-chat","history":"h2","from_seq":0,"to_seq":2,"items":[{"id":"line1#0","seq":1,"kind":"text","role":"assistant","text":"live"}]}`))
	f.mu.Lock()
	told := append([]told(nil), f.told...)
	f.mu.Unlock()
	found := false
	for _, event := range told {
		if event.event != devicewire.EventNode {
			continue
		}
		var p devicewire.NodeEvent
		_ = json.Unmarshal(event.params, &p)
		if strings.Contains(string(p.Params), "segment:two:line1#0") {
			var page struct {
				SessionID string `json:"session_id"`
				historyPage
			}
			_ = json.Unmarshal(p.Params, &page)
			if p.MachineID != "server" || page.SessionID != "side-chat" || intValue(page.FromSeq) != 6 || intValue(page.ToSeq) != 8 || page.History != c.History {
				t.Fatalf("live span: %s", p.Params)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("active segment live event did not reach public transport")
	}
	f.mu.Lock()
	count := len(f.told)
	f.mu.Unlock()
	s.NodeEvent("server", "hook.event", json.RawMessage(`{"session_id":"side-chat","event":"Stop"}`))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.told) != count {
		t.Fatal("retired hook settled the active turn")
	}
}

func TestLogicalUnknownTargetAndPausedSwitchSetting(t *testing.T) {
	s, f, d, _ := logicalPair(t, "pi")
	ctx := t.Context()
	// Resolve the prepared operation before trying an explicit new one.
	if err := d.ResolveConversationSwitch(ctx, "server", "side-chat", "operation", "rejected", ""); err != nil {
		t.Fatal(err)
	}
	f.answerNode("assistant.segment.check", func(_ string, _ json.RawMessage) (any, error) {
		return segmentTarget{Engine: "future-unknown", Prepare: true, SupervisionPolicy: true}, nil
	})
	_, err := s.beginLogicalSwitch(ctx, "server", "side-chat", logicalSwitchRequest{OperationID: "new", ExpectedEpoch: 1, MachineID: "server", Launcher: "unknown"})
	if err == nil {
		t.Fatal("unknown engine allowed")
	}
	if err = d.PutSetting(ctx, conversationSegmentsKey, map[string]bool{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	_, err = s.beginLogicalSwitch(ctx, "server", "side-chat", logicalSwitchRequest{OperationID: "new", ExpectedEpoch: 1, MachineID: "server", Launcher: "pi"})
	if err == nil {
		t.Fatal("switch-disable did not stop new switches")
	}
	if c, err := d.LogicalConversation(ctx, "server", "side-chat"); err != nil || c == nil {
		t.Fatal("switch-disable removed mappings")
	}
}

func TestLogicalInDoubtSendIsNeverRepeated(t *testing.T) {
	s, f, d, _ := logicalPair(t, "pi")
	ctx := t.Context()
	activatePair(t, d)
	count := 0
	f.answerNode(DeviceAssistantSend, func(_ string, _ json.RawMessage) (any, error) {
		count++
		return nil, errors.New("fake connection lost after acceptance")
	})
	f.answerNode("assistant.segment.input", func(_ string, _ json.RawMessage) (any, error) { return map[string]bool{"accepted": false}, nil })
	for _, client := range []string{"one", "two"} {
		raw, _ := json.Marshal(map[string]any{"session_id": "side-chat", "client_msg_id": client, "text": "same words"})
		if _, _, err := s.logicalCall(ctx, "server", DeviceAssistantSend, raw); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := d.LogicalConversation(ctx, "server", "side-chat")
	_ = s.drainLogicalInput(ctx, d, c)
	for range 3 {
		c, _ = d.LogicalConversation(ctx, "server", "side-chat")
		if err := s.drainLogicalInput(context.Background(), d, c); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("ambiguous input repeated or overtaken %d", count)
	}
}

func TestLogicalSummaryWaitsOnPausedFakeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	_, err := waitLogicalSummary(ctx, func() (assistantwire.ModelCompleteResult, error) {
		calls++
		cancel()
		return assistantwire.ModelCompleteResult{}, &rpc.Error{Code: assistantwire.CodeModelPaused, Message: "fake quota pause", Data: json.RawMessage(`{"until":"2099-01-01T00:00:00Z"}`)}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("pause polled the fake model: %d %v", calls, err)
	}
	calls = 0
	out, err := waitLogicalSummary(t.Context(), func() (assistantwire.ModelCompleteResult, error) {
		calls++
		return assistantwire.ModelCompleteResult{Text: "fake summary"}, nil
	})
	if err != nil || calls != 1 || out.Text != "fake summary" {
		t.Fatalf("summary: %+v %v", out, err)
	}
}
