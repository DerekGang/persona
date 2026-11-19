package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestDisplayMirrorUpdatesAndResolvesTogether(t *testing.T) {
	h := newTestHub(t)
	c := &client{out: make(chan []byte, 8)}
	addTestClient(h, c)
	origin := proto.Interaction{ID: "ix_origin", SessionID: "side", SessionTitle: "Originating conversation",
		Kind: proto.IxPermission, Summary: "before", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC()}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.SessionID = "ix_display", origin.ID, "main"
	reportInteraction(t, h, origin)
	reportInteraction(t, h, mirror)
	for range 2 {
		_ = wireReceive(t, c)
	}

	origin.Summary = "after"
	origin.ExpiresAt = origin.ExpiresAt.Add(time.Hour)
	reportInteraction(t, h, origin)
	var updated bool
	for range 2 {
		var event struct {
			Type        string            `json:"type"`
			Interaction proto.Interaction `json:"interaction"`
		}
		if json.Unmarshal(wireReceive(t, c), &event) != nil || event.Type != "interaction" {
			t.Fatal("expected an interaction update")
		}
		if event.Interaction.ID == mirror.ID {
			updated = true
			if event.Interaction.MirrorOf != origin.ID || event.Interaction.SessionID != "main" ||
				event.Interaction.SessionTitle != origin.SessionTitle || event.Interaction.Summary != "after" ||
				!event.Interaction.ExpiresAt.Equal(origin.ExpiresAt) {
				t.Fatalf("copy did not follow the original: %+v", event.Interaction)
			}
		}
	}
	if !updated {
		t.Fatal("the original update did not refresh its display copy")
	}

	reportResolved(t, h, origin.ID, "")
	resolved := map[string]bool{}
	for range 2 {
		var event struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(wireReceive(t, c), &event) != nil || event.Type != "interaction_resolved" {
			t.Fatal("expected a resolution event")
		}
		resolved[event.ID] = true
	}
	if !resolved[origin.ID] || !resolved[mirror.ID] || len(resolved) != 2 {
		t.Fatalf("resolution withdrew %v, want original and copy", resolved)
	}
	if got := h.pendingInteractions(); len(got) != 0 {
		t.Fatalf("resolved copies remain pending: %+v", got)
	}

	// An orphaned copy cannot be reintroduced after its origin has resolved.
	reportInteraction(t, h, mirror)
	select {
	case event := <-c.out:
		t.Fatalf("orphaned copy was broadcast: %s", event)
	default:
	}
	if got := h.pendingInteractions(); len(got) != 0 {
		t.Fatalf("orphaned copy was accepted: %+v", got)
	}
}

func TestDisplayMirrorSnapshotRejectsPrivateAndOrphanedCopies(t *testing.T) {
	h := newTestHub(t)
	origin := proto.Interaction{ID: "ix_private", SessionID: "side", Kind: proto.IxPermission, Private: true}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.SessionID = "ix_private_copy", origin.ID, "main"
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, []proto.Interaction{mirror, origin})
	got := h.pendingInteractions()
	if len(got) != 1 || got[0].ID != origin.ID {
		t.Fatalf("private snapshot accepted a display copy: %+v", got)
	}
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, []proto.Interaction{mirror})
	if got := h.pendingInteractions(); len(got) != 0 {
		t.Fatalf("orphaned snapshot accepted a display copy: %+v", got)
	}
}

func TestDisplayMirrorSnapshotKeepsLiveCopyWithoutManagerCopy(t *testing.T) {
	h := newTestHub(t)
	origin := proto.Interaction{ID: "ix_existing", SessionID: "side", Kind: proto.IxPermission}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.SessionID = "ix_existing_copy", origin.ID, "main"
	mirror.Manager = assistantwire.AssistantID
	origin.Manager = assistantwire.AssistantID
	h.reported.interactions = map[string]proto.Interaction{origin.ID: origin, mirror.ID: mirror}

	// A reconnect after the preference was turned off for new cards still carries
	// the original alone. The hub must not withdraw the copy it already showed.
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, []proto.Interaction{origin})
	got := h.pendingInteractions()
	if len(got) != 2 {
		t.Fatalf("reconnect withdrew the live display copy: %+v", got)
	}
	ids := map[string]bool{}
	for _, ix := range got {
		ids[ix.ID] = true
	}
	if !ids[origin.ID] || !ids[mirror.ID] {
		t.Fatalf("reconnect cards = %v, want original and existing copy", ids)
	}
}

func TestDisplayMirrorActionsRelayTheAuthoritativePath(t *testing.T) {
	h := newTestHub(t)
	const token = "test-mirror-owner-token"
	h.store = &tokenStore{memStore: *newMemStore(), rows: map[string]db.DeviceToken{
		hashToken(token): {ID: "device-1", Name: "Trusted device", Role: roleFull},
	}, rev: map[string]bool{}}
	hubIn, assistantOut := io.Pipe()
	assistantIn, hubOut := io.Pipe()
	hp, ap := rpc.New("h", hubOut), rpc.New("a", assistantOut)
	got := make(chan assistantwire.HTTPRequest, 2)
	ap.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != assistantwire.MethodHTTP {
			return nil, errors.New("unexpected " + method)
		}
		var req assistantwire.HTTPRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		got <- req
		return assistantwire.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	go hp.Serve(ctx, hubIn)
	go ap.Serve(ctx, assistantIn)
	t.Cleanup(func() {
		cancel()
		hp.Close(io.EOF)
		ap.Close(io.EOF)
		_ = hubIn.Close()
		_ = assistantIn.Close()
		_ = hubOut.Close()
		_ = assistantOut.Close()
	})
	h.bus.current = map[string]*busConn{busAssistant: {peer: hp}}
	origin := proto.Interaction{ID: "ix_authoritative", MachineID: "box", SessionID: "side", Kind: proto.IxPermission,
		Manager: assistantwire.AssistantID, CanClarify: true}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.SessionID = "ix_main_copy", origin.ID, "main"
	h.reported.interactions = map[string]proto.Interaction{origin.ID: origin, mirror.ID: mirror}
	routes := h.routes()
	for _, tc := range []struct{ action, body string }{
		{"resolve", `{"allow":true}`},
		{"clarify", `{"text":"Why?","client_msg_id":"mirror-question"}`},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+mirror.ID+"/"+tc.action, strings.NewReader(tc.body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.action, w.Code, w.Body)
		}
		select {
		case req := <-got:
			want := "/api/v1/interactions/" + origin.ID + "/" + tc.action
			if req.Path != want {
				t.Fatalf("%s forwarded path = %q, want %q", tc.action, req.Path, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not reach the manager", tc.action)
		}
	}
}
