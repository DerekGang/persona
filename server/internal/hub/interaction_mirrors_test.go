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
	c := &client{out: make(chan []byte, 8), urlFeatures: map[string]bool{featureApprovalMirrors: true}}
	addTestClient(h, c)
	origin := proto.Interaction{ID: "ix_origin", SessionID: "side", SessionTitle: "Originating conversation",
		Kind: proto.IxPermission, MirrorMain: true, Summary: "before", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC()}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
	forged := mirror
	forged.ID = "ix_runtime_chosen"
	if validDisplayMirror(origin, forged) {
		t.Fatal("a runtime-chosen display ID passed hub validation")
	}
	mirror.Summary = "caller-supplied text"
	reportInteraction(t, h, origin)
	reportInteraction(t, h, mirror)
	for range 2 {
		_ = wireReceive(t, c)
	}
	for _, ix := range h.pendingInteractions() {
		if ix.ID == mirror.ID && ix.Summary != origin.Summary {
			t.Fatalf("display content did not come from the authoritative card: %+v", ix)
		}
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
	origin := proto.Interaction{ID: "ix_private", SessionID: "side", Kind: proto.IxPermission, MirrorMain: true, Private: true}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
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
	origin := proto.Interaction{ID: "ix_existing", SessionID: "side", Kind: proto.IxPermission, MirrorMain: true, Summary: "before", ExpiresAt: time.Now().Add(time.Minute).UTC()}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
	mirror.Manager = assistantwire.AssistantID
	origin.Manager = assistantwire.AssistantID
	h.reported.interactions = map[string]proto.Interaction{origin.ID: origin, mirror.ID: mirror}

	// A reconnect after the preference was turned off for new cards still carries
	// the original alone. The hub must not withdraw the copy it already showed,
	// and its retained display state must follow the original's latest deadline.
	updated := origin
	updated.Summary = "after"
	updated.ExpiresAt = origin.ExpiresAt.Add(time.Hour)
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, []proto.Interaction{updated})
	got := h.pendingInteractions()
	if len(got) != 2 {
		t.Fatalf("reconnect withdrew the live display copy: %+v", got)
	}
	ids := map[string]bool{}
	for _, ix := range got {
		ids[ix.ID] = true
		if ix.ID == mirror.ID && (ix.Summary != "after" || !ix.ExpiresAt.Equal(updated.ExpiresAt)) {
			t.Fatalf("retained copy was not refreshed from the original: %+v", ix)
		}
	}
	if !ids[origin.ID] || !ids[mirror.ID] {
		t.Fatalf("reconnect cards = %v, want original and existing copy", ids)
	}
}

func TestDisplayMirrorSnapshotRefreshesContentAndDeduplicatesIDs(t *testing.T) {
	h := newTestHub(t)
	c := &client{out: make(chan []byte, 8), urlFeatures: map[string]bool{featureApprovalMirrors: true}}
	addTestClient(h, c)
	deadline := time.Now().Add(10 * time.Minute).UTC()
	forgedDeadline := deadline.Add(time.Hour)
	origin := proto.Interaction{ID: "ix_snapshot", SessionID: "side", Kind: proto.IxPermission, MirrorMain: true,
		Summary: "authoritative", ExpiresAt: time.Now().Add(time.Minute).UTC(), Deadline: &deadline,
		Questions: []proto.Question{{Question: "authoritative question", Options: []proto.QuestionOption{{Label: "Allow"}}}},
		ToolInput: json.RawMessage(`{"command":"authoritative"}`)}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
	forged := mirror
	forged.Summary = "forged"
	forged.ExpiresAt = origin.ExpiresAt.Add(time.Hour)
	forged.Deadline = &forgedDeadline
	forged.Questions = []proto.Question{{Question: "forged question", Options: []proto.QuestionOption{{Label: "Deny"}}}}
	forged.ToolInput = json.RawMessage(`{"command":"forged"}`)
	// The last duplicate has always won in the pending map. The ordered event
	// snapshot must contain that same single entry, rebuilt from the original.
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, []proto.Interaction{origin, mirror, forged})
	if got := len(c.out); got != 2 {
		t.Fatalf("duplicate snapshot produced %d events, want original and one copy", got)
	}
	for range 2 {
		_ = wireReceive(t, c)
	}
	got := h.pendingInteractions()
	if len(got) != 2 {
		t.Fatalf("snapshot cards = %+v, want original and one copy", got)
	}
	for _, ix := range got {
		if ix.ID != mirror.ID {
			continue
		}
		if ix.Summary != origin.Summary || !ix.ExpiresAt.Equal(origin.ExpiresAt) || ix.Deadline == nil || !ix.Deadline.Equal(deadline) ||
			len(ix.Questions) != 1 || ix.Questions[0].Question != "authoritative question" || string(ix.ToolInput) != string(origin.ToolInput) {
			t.Fatalf("snapshot copy kept caller-controlled content: %+v", ix)
		}
	}
}

func TestApprovalMirrorsRequireClientCapabilityEverywhere(t *testing.T) {
	h := newTestHub(t)
	legacy := &client{out: make(chan []byte, 16)}
	capable := &client{out: make(chan []byte, 16)}
	addTestClient(h, legacy, capable)
	h.onClientHello(capable, []byte(`{"platform":"android","version":"test","features":["approval_mirrors"]}`))
	<-capable.out // client_hello_ack
	origin := proto.Interaction{ID: "ix_capability", SessionID: "side", Kind: proto.IxPermission, MirrorMain: true}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
	reportInteraction(t, h, origin)
	reportInteraction(t, h, mirror)
	for range 2 {
		_ = wireReceive(t, capable)
	}
	var event struct {
		Type        string            `json:"type"`
		Interaction proto.Interaction `json:"interaction"`
	}
	if json.Unmarshal(wireReceive(t, legacy), &event) != nil || event.Interaction.ID != origin.ID {
		t.Fatalf("legacy live event = %+v, want only the original", event)
	}
	select {
	case raw := <-legacy.out:
		t.Fatalf("legacy client received a display copy: %s", raw)
	default:
	}

	// The pre-hello snapshot is authoritative-only. A later hello fills in the
	// copies without repeating the originals; a URL-capable client gets both.
	joining := &client{out: make(chan []byte, 16)}
	h.sendPendingInteractions(joining, false)
	if json.Unmarshal(wireReceive(t, joining), &event) != nil || event.Interaction.ID != origin.ID || len(joining.out) != 0 {
		t.Fatalf("legacy initial snapshot = %+v", event)
	}
	h.onClientHello(joining, []byte(`{"features":["approval_mirrors"]}`))
	<-joining.out // client_hello_ack
	if json.Unmarshal(wireReceive(t, joining), &event) != nil || event.Interaction.ID != mirror.ID || len(joining.out) != 0 {
		t.Fatalf("post-hello copy completion = %+v", event)
	}
	joiningCapable := &client{out: make(chan []byte, 16), urlFeatures: map[string]bool{featureApprovalMirrors: true}}
	h.sendPendingInteractions(joiningCapable, false)
	for range 2 {
		_ = wireReceive(t, joiningCapable)
	}
	if len(joiningCapable.out) != 0 {
		t.Fatal("capable initial snapshot omitted or duplicated a card")
	}

	for _, tc := range []struct {
		header string
		want   int
	}{{"", 1}, {"android/test", 2}, {"ios/test", 1}} {
		r := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/interactions", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		r.Header.Set("X-Persona-Client", tc.header)
		w := httptest.NewRecorder()
		h.routes().ServeHTTP(w, r)
		var body struct {
			Interactions []proto.Interaction `json:"interactions"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Interactions) != tc.want {
			t.Fatalf("REST %q: %d %s, want %d cards", tc.header, w.Code, w.Body.String(), tc.want)
		}
	}

	// Event replay uses the same opt-in as live delivery. In particular, an old
	// client cannot catch up on a copy it never knew how to deduplicate.
	h.mu.Lock()
	stream := h.events.stream
	replayLegacy := &client{out: make(chan []byte, 16)}
	replayCapable := &client{out: make(chan []byte, 16), urlFeatures: map[string]bool{featureApprovalMirrors: true}}
	h.helloLocked(replayLegacy, map[string]any{"type": "hello"}, stream, 0, true)
	h.helloLocked(replayCapable, map[string]any{"type": "hello"}, stream, 0, true)
	h.mu.Unlock()
	<-replayLegacy.out  // hello
	<-replayCapable.out // hello
	if json.Unmarshal(wireReceive(t, replayLegacy), &event) != nil || event.Interaction.ID != origin.ID || len(replayLegacy.out) != 0 {
		t.Fatalf("legacy replay included a display copy: %+v", event)
	}
	seen := map[string]bool{}
	for range 2 {
		if json.Unmarshal(wireReceive(t, replayCapable), &event) != nil {
			t.Fatal("bad capable replay event")
		}
		seen[event.Interaction.ID] = true
	}
	if !seen[origin.ID] || !seen[mirror.ID] || len(replayCapable.out) != 0 {
		t.Fatalf("capable replay cards = %v", seen)
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
		Manager: assistantwire.AssistantID, MirrorMain: true, CanClarify: true}
	mirror := origin
	mirror.ID, mirror.MirrorOf, mirror.MirrorMain, mirror.SessionID = proto.ApprovalMirrorID(origin.ID), origin.ID, false, "main"
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
