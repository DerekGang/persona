package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
	"github.com/nwverse/persona/server/internal/proto"
)

// Real manager and hub connected over the bus: omission from FleetUp is an
// authoritative resolution, so hiding must never remove the pending card there.
func TestHiddenDirectoryFleetUpRetainsPendingCard(t *testing.T) {
	base := os.Getenv(dbtest.Env)
	if base == "" {
		t.Skip(dbtest.Env + " not set")
	}
	url, cleanup, err := dbtest.Own(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	database, err := db.Open(context.Background(), url, db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "box", Name: "Computer"})
	registerMachine(h, m)
	m.sessions = []proto.Session{{MachineID: "box", SessionID: "blocked", Cwd: "/work/noisy", Status: proto.StatusBlocked}}
	a := connectAssistant(t, h, database)
	app := &client{out: make(chan []byte, 256), subs: map[[2]string]bool{}, role: roleFull}
	h.mu.Lock()
	h.clients[app] = struct{}{}
	h.mu.Unlock()
	t.Cleanup(func() { h.mu.Lock(); delete(h.clients, app); h.mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ix := proto.Interaction{ID: "ix_hidden_transport", MachineID: "box", SessionID: "blocked", Kind: proto.IxPermission, Summary: "Approve work"}
	raw, _ := json.Marshal(map[string]any{"interaction": ix})
	done := make(chan struct{})
	result := make(chan proto.Resolution, 1)
	go func() {
		defer close(done)
		value, _, _ := a.svc.NodeRequest(ctx, "box", "interaction.create", raw)
		if r, ok := value.(proto.Resolution); ok {
			result <- r
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("node request did not stop")
		}
	})
	eventually(t, 5*time.Second, "pending card at hub", func() bool { return len(h.pendingInteractions()) == 1 })
	for len(app.out) > 0 {
		<-app.out
	}
	save := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		a.svc.ServeHTTP(w, httptest.NewRequest("PUT", "/api/v1/assistant/hidden-agent-dirs", strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
	}
	save(`{"rules":[{"machine_id":"box","pattern":"/work/noisy","enabled":true}]}`)
	// The ordered bus places a following marker after FleetUp, avoiding a sleep
	// that could check hub state before the authoritative snapshot arrived.
	a.svc.FleetUp(ctx)
	marker := proto.Interaction{ID: "ix_transport_barrier", MachineID: "box", SessionID: "barrier", Kind: proto.IxPermission}
	markerRaw, _ := json.Marshal(map[string]any{"interaction": marker})
	markerCtx, markerCancel := context.WithCancel(ctx)
	defer markerCancel()
	go func() { _, _, _ = a.svc.NodeRequest(markerCtx, "box", "interaction.create", markerRaw) }()
	eventually(t, 5*time.Second, "FleetUp barrier", func() bool {
		for _, c := range h.pendingInteractions() {
			if c.ID == marker.ID {
				return true
			}
		}
		return false
	})
	retained := false
	for _, c := range h.pendingInteractions() {
		retained = retained || c.ID == ix.ID
	}
	if !retained {
		t.Fatal("FleetUp forgot a hidden pending card at the real hub")
	}
	for len(app.out) > 0 {
		event := <-app.out
		var p struct{ Type, ID string }
		_ = json.Unmarshal(event, &p)
		if p.Type == "interaction_resolved" && p.ID == ix.ID {
			t.Fatal("hiding/reconnect resolved the blocked agent's card")
		}
	}
	save(`{"rules":[]}`)
	received := false
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !received {
		select {
		case b := <-app.out:
			var p struct {
				Type        string
				Interaction proto.Interaction
			}
			_ = json.Unmarshal(b, &p)
			received = p.Type == "interaction" && p.Interaction.ID == ix.ID
		case <-deadline.C:
			t.Fatal("unhide did not deliver the pending card to the connected client")
		}
	}
	retained = false
	for _, card := range h.pendingInteractions() {
		retained = retained || card.ID == ix.ID
	}
	if !retained {
		t.Fatal("unhide lost the actionable card")
	}
	if err := h.resolve(ix.ID, proto.Resolution{Allow: new(true)}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if r.Allow == nil || !*r.Allow {
			t.Fatalf("node did not receive approval: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retained card could not unblock the node")
	}
}
