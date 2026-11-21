package db

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/conversationlog"
)

func prepareLogicalSwitch(t *testing.T, d *DB) ConversationSwitch {
	t.Helper()
	ctx := t.Context()
	if err := d.AdoptConversation(ctx, "root-machine", "root-chat", "first", "claude", "source-one"); err != nil {
		t.Fatal(err)
	}
	op := ConversationSwitch{OperationID: "switch-one", ExpectedEpoch: 1, SourceSegment: "first", Request: json.RawMessage(`{"launcher":"fake-target"}`)}
	if err := d.BeginConversationSwitch(ctx, "root-machine", "root-chat", op); err != nil {
		t.Fatal(err)
	}
	if err := d.BeginConversationSwitch(ctx, "root-machine", "root-chat", op); err != nil {
		t.Fatalf("operation replay: %v", err)
	}
	other := op
	other.OperationID = "another"
	if err := d.BeginConversationSwitch(ctx, "root-machine", "root-chat", other); !errors.Is(err, ErrConversationSwitchBusy) {
		t.Fatalf("two unfinished switches: %v", err)
	}
	if err := d.PrepareConversationSegment(ctx, "root-machine", "root-chat", op.OperationID, conversationlog.Segment{ID: "second", MachineID: "target-machine", SessionID: "target-chat", Engine: "codex", SourceHistory: "source-two"}, 100); err != nil {
		t.Fatal(err)
	}
	return op
}

func TestLogicalConversationActivationCASAcrossDatabaseConnections(t *testing.T) {
	d, url := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	other, err := Open(t.Context(), url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, database := range []*DB{d, other} {
		wg.Add(1)
		go func(database *DB) {
			defer wg.Done()
			<-start
			results <- database.ActivateConversationSegment(t.Context(), "root-machine", "root-chat", op.OperationID, 1)
		}(database)
	}
	close(start)
	wg.Wait()
	close(results)
	passed, stale := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, ErrConversationEpoch) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || stale != 1 {
		t.Fatalf("activation race: passed=%d stale=%d", passed, stale)
	}
	c, err := other.LogicalConversation(t.Context(), "target-machine", "target-chat")
	if err != nil || c == nil {
		t.Fatalf("restart/alias lookup: %+v %v", c, err)
	}
	active, ok := c.Active()
	if !ok || active.ID != "second" || active.Offset != 100 || c.Epoch != 2 || c.History != "source-one" || c.MachineID != "root-machine" || c.SessionID != "root-chat" {
		t.Fatalf("activation changed public identity: %+v", c)
	}
	if c.Segments[0].ClosePending {
		t.Fatal("source closed before the first accepted turn")
	}
	if err := d.WithConversationEpoch(t.Context(), c.MachineID, c.SessionID, 1, func(*LogicalConversation) error { t.Fatal("stale writer executed"); return nil }); !errors.Is(err, ErrConversationEpoch) {
		t.Fatal(err)
	}
	if err := d.ResolveConversationSwitch(t.Context(), c.MachineID, c.SessionID, op.OperationID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	c, err = d.LogicalConversation(t.Context(), c.MachineID, c.SessionID)
	if err != nil || !c.Segments[0].ClosePending || c.Switch != nil {
		t.Fatalf("source not retired after acceptance: %+v %v", c, err)
	}
}

func TestLogicalConversationFirstTurnRejectionRestoresSourceAtNewEpoch(t *testing.T) {
	d, _ := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	ctx := t.Context()
	if err := d.ActivateConversationSegment(ctx, "root-machine", "root-chat", op.OperationID, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.ResolveConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, "rejected", "fake policy refusal"); err != nil {
		t.Fatal(err)
	}
	c, err := d.LogicalConversation(ctx, "root-machine", "root-chat")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := c.Active()
	if !ok || a.ID != "first" || a.FrozenEnd != nil || c.Epoch != 3 || c.Segments[1].State != "abandoned" || !c.Segments[1].ClosePending {
		t.Fatalf("rollback reused an epoch or lost source: %+v", c)
	}
	op.OperationID = "next-switch"
	op.ExpectedEpoch = 3
	if err := d.BeginConversationSwitch(ctx, c.MachineID, c.SessionID, op); err != nil {
		t.Fatal(err)
	}
	if err := d.PrepareConversationSegment(ctx, c.MachineID, c.SessionID, op.OperationID, conversationlog.Segment{ID: "third", MachineID: "target-machine", SessionID: "next-chat", Engine: "agy"}, 110); err != nil {
		t.Fatal(err)
	}
	c, err = d.LogicalConversation(ctx, c.MachineID, c.SessionID)
	if err != nil || c.Segments[2].Ordinal != 3 || c.Segments[2].Offset != 110 {
		t.Fatalf("reused abandoned ordinal: %+v %v", c, err)
	}
}

func TestLogicalConversationQueueRetainsInputIdentityAndInDoubtFence(t *testing.T) {
	d, url := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	ctx := t.Context()
	payload := json.RawMessage(`{"text":"same words","client_msg_id":"input-one"}`)
	for range 2 {
		if err := d.EnqueueConversationInput(ctx, "root-machine", "root-chat", "input-one", 1, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.EnqueueConversationInput(ctx, "root-machine", "root-chat", "input-two", 1, json.RawMessage(`{"text":"same words","client_msg_id":"input-two"}`)); err != nil {
		t.Fatal(err)
	}
	if row, err := d.ClaimConversationInput(ctx, "root-machine", "root-chat", 1); err != nil || row != nil {
		t.Fatalf("input escaped preparation: %+v %v", row, err)
	}
	if err := d.ActivateConversationSegment(ctx, "root-machine", "root-chat", op.OperationID, 1); err != nil {
		t.Fatal(err)
	}
	row, err := d.ClaimConversationInput(ctx, "root-machine", "root-chat", 2)
	if err != nil || row == nil || row.ClientMsgID != "input-one" || string(row.Payload) != string(payload) {
		t.Fatalf("input identity changed %+v %v", row, err)
	}
	restarted, err := Open(ctx, url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if row, err := restarted.ClaimConversationInput(ctx, "root-machine", "root-chat", 2); err != nil || row != nil {
		t.Fatalf("restart repeated a sending turn %+v %v", row, err)
	}
	if err := d.ResolveConversationInput(ctx, "root-machine", "root-chat", "input-one", "in_doubt"); err != nil {
		t.Fatal(err)
	}
	if row, err := restarted.ClaimConversationInput(ctx, "root-machine", "root-chat", 2); err != nil || row != nil {
		t.Fatalf("in-doubt head was overtaken %+v %v", row, err)
	}
	if err := d.ResolveConversationInput(ctx, "root-machine", "root-chat", "input-one", "accepted"); err != nil {
		t.Fatal(err)
	}
	row, err = d.ClaimConversationInput(ctx, "root-machine", "root-chat", 2)
	if err != nil || row == nil || row.ClientMsgID != "input-two" {
		t.Fatalf("distinct repeated input lost %+v %v", row, err)
	}
}
