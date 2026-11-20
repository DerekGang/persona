package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const mirrorTestAssistantDir = "/home/u/.persona/assistant"

func newMirrorFleetTest(t *testing.T) *fleetTest {
	t.Helper()
	ft := newFleetTest(t)
	ft.f.mu.Lock()
	ft.f.hello.ApprovalMirrors = true
	ft.f.mu.Unlock()
	setSettings(t, ft.s, func(v *Settings) {
		v.MachineID, v.Main, v.MainMachineID = "mac", "main", "mac"
	})
	ft.sessions("mac",
		proto.Session{SessionID: "main", Cwd: mirrorTestAssistantDir, Assistant: true},
		proto.Session{SessionID: "side", Cwd: mirrorTestAssistantDir, Assistant: true},
		proto.Session{SessionID: "agent", Cwd: "/work/example", Agent: "claude"},
		proto.Session{SessionID: "health", Cwd: "/home/u/.persona/health"},
	)
	return ft
}

func mirrorTestCard(id string) proto.Interaction {
	ix := resumable(id)
	ix.SessionID = "side"
	ix.Cwd = mirrorTestAssistantDir
	ix.SessionTitle = "Originating conversation"
	ix.ToolName = "Bash"
	ix.ToolInput = json.RawMessage(`{"command":"printf ok"}`)
	return ix
}

func TestApprovalMirrorDisplayKeepsOneAuthoritativeRequest(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ctx := context.Background()
	ix := mirrorTestCard("ix_mirror")
	done := make(chan struct{})
	go func() {
		_, _ = ft.s.waitInteraction(ctx, ix)
		close(done)
	}()
	ft.waitPending(1)
	waitFor(t, "original and main display copy", func() bool {
		return len(ft.toldOf(assistantwire.EventFleetInteraction)) == 2
	})
	var original, mirror proto.Interaction
	for _, raw := range ft.toldOf(assistantwire.EventFleetInteraction) {
		var p assistantwire.FleetInteraction
		if json.Unmarshal(raw, &p) != nil {
			t.Fatalf("bad interaction event: %s", raw)
		}
		switch p.Interaction.ID {
		case ix.ID:
			original = p.Interaction
		case approvalMirrorID(ix.ID):
			mirror = p.Interaction
		default:
			t.Fatalf("unexpected display card: %+v", p.Interaction)
		}
	}
	if original.MirrorOf != "" || !original.MirrorMain || mirror.MirrorMain || mirror.MirrorOf != ix.ID || mirror.ID == ix.ID {
		t.Fatalf("authoritative/copy identity: original=%+v mirror=%+v", original, mirror)
	}
	if mirror.MachineID != "mac" || mirror.SessionID != "main" || mirror.SessionTitle != ix.SessionTitle {
		t.Fatalf("mirror destination or originating title: %+v", mirror)
	}
	if got := ft.s.pendingInteractions(); len(got) != 1 || got[0].ID != ix.ID {
		t.Fatalf("aggregate pending operations = %+v, want only %s", got, ix.ID)
	}
	if got := ft.s.pendingDisplayInteractions(ctx); len(got) != 2 {
		t.Fatalf("display snapshot = %+v, want original and copy", got)
	}
	ft.s.FleetUp(ctx)
	var snapshot assistantwire.FleetInteractions
	for _, raw := range ft.toldOf(assistantwire.EventFleetInteractions) {
		if json.Unmarshal(raw, &snapshot) == nil && len(snapshot.Interactions) != 0 {
			break
		}
	}
	if len(snapshot.Interactions) != 2 {
		t.Fatalf("reconnect snapshot = %+v, want original and copy", snapshot.Interactions)
	}
	yes := true
	if err := ft.s.resolve(ix.ID, proto.Resolution{Allow: &yes}); err != nil {
		t.Fatal(err)
	}
	<-done
	ft.waitPending(0)
}

func TestApprovalMirrorEligibilityAndSettingOff(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ctx := context.Background()
	base := mirrorTestCard("ix_eligible")
	for _, tc := range []struct {
		name string
		edit func(*proto.Interaction)
		want bool
	}{
		{name: "non-main assistant permission", want: true},
		{name: "main conversation", edit: func(ix *proto.Interaction) { ix.SessionID = "main" }},
		{name: "private", edit: func(ix *proto.Interaction) { ix.Private = true }},
		{name: "health", edit: func(ix *proto.Interaction) { ix.SessionID, ix.Cwd = "health", "/home/u/.persona/health" }},
		{name: "ordinary agent", edit: func(ix *proto.Interaction) { ix.SessionID, ix.Cwd = "agent", "/work/example" }},
		{name: "not a permission", edit: func(ix *proto.Interaction) { ix.Kind = proto.IxQuestion }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ix := base
			// A same-conversation original cannot have a valid existing main
			// copy. Every other case verifies that persisted display intent
			// cannot make an otherwise excluded card eligible.
			ix.MirrorMain = tc.name != "main conversation"
			if tc.edit != nil {
				tc.edit(&ix)
			}
			_, got := ft.s.mainApprovalMirror(ctx, ix)
			if got != tc.want {
				t.Fatalf("mirror = %v, want %v", got, tc.want)
			}
		})
	}
	mirror, ok := ft.s.mainApprovalMirror(ctx, base)
	if !ok {
		t.Fatal("eligible card has no display copy")
	}
	if _, ok := ft.s.mainApprovalMirror(ctx, mirror); ok {
		t.Fatal("a display copy must not recursively mirror itself")
	}
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = new(false) })
	view := ft.s.view(ctx, false)
	if _, ok := ft.s.mainApprovalMirror(ctx, base); ok {
		t.Fatal("setting off created a copy for a new card")
	}
	existing := base
	existing.MirrorMain = true
	if _, ok := ft.s.mainApprovalMirror(ctx, existing); !ok {
		t.Fatal("setting off withdrew an already-created copy")
	}
	if view.ApprovalMirrorMain {
		t.Fatal("setting off was not reported in effect")
	}
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = nil })
	if !ft.s.view(ctx, false).ApprovalMirrorMain {
		t.Fatal("unset preference must default on")
	}
	ft.f.mu.Lock()
	ft.f.hello.ApprovalMirrors = false
	ft.f.mu.Unlock()
	if _, ok := ft.s.mainApprovalMirror(ctx, base); ok {
		t.Fatal("an older hub without mirror support received a display copy")
	}
}

func TestNodeInteractionCannotDeclareMirrorIdentity(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ix := mirrorTestCard("ix_runtime_forgery")
	ix.SessionID = "main"
	ix.MirrorOf = "ix_some_other_approval"
	ix.MirrorMain = true
	done := make(chan error, 1)
	go func() {
		_, err := ft.s.nodeInteraction(context.Background(), ix)
		done <- err
	}()
	ft.waitPending(1)
	pending := ft.s.pendingInteractions()
	if len(pending) != 1 || pending[0].MirrorOf != "" || pending[0].MirrorMain {
		t.Fatalf("runtime mirror fields survived normalization: %+v", pending)
	}
	deny := false
	if err := ft.s.resolve(ix.ID, proto.Resolution{Allow: &deny}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the normalized interaction did not finish")
	}
}

func TestCodexInteractionCannotDeclareMirrorIdentity(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ix := mirrorTestCard("ix_codex_forgery")
	ix.SessionID = "main" // no manager-created copy: surviving fields must be the node's
	ix.MirrorOf = "ix_some_other_approval"
	ix.MirrorMain = true
	listCodex(ft.s, ix)
	pending := ft.s.pendingInteractions()
	if len(pending) != 1 || pending[0].MirrorOf != "" || pending[0].MirrorMain {
		t.Fatalf("Codex mirror fields survived normalization: %+v", pending)
	}
	waitFor(t, "normalized Codex card", func() bool {
		return len(ft.toldOf(assistantwire.EventFleetInteraction)) >= 1
	})
	for _, raw := range ft.toldOf(assistantwire.EventFleetInteraction) {
		var p assistantwire.FleetInteraction
		if json.Unmarshal(raw, &p) != nil || p.Interaction.MirrorOf != "" || p.Interaction.MirrorMain {
			t.Fatalf("Codex card declared manager-owned display identity: %s", raw)
		}
	}
}

func TestCodexApprovalMirrorRecordsAuthoritativeIntent(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ix := mirrorTestCard("ix_codex_mirror")
	listCodex(ft.s, ix)
	pending := ft.s.pendingInteractions()
	if len(pending) != 1 || pending[0].MirrorOf != "" || !pending[0].MirrorMain {
		t.Fatalf("Codex original has no durable manager-owned display intent: %+v", pending)
	}
	waitFor(t, "Codex original and display copy", func() bool {
		return len(ft.toldOf(assistantwire.EventFleetInteraction)) >= 2
	})
	var original, mirror proto.Interaction
	for _, raw := range ft.toldOf(assistantwire.EventFleetInteraction) {
		var p assistantwire.FleetInteraction
		if json.Unmarshal(raw, &p) != nil {
			t.Fatalf("bad Codex interaction event: %s", raw)
		}
		switch p.Interaction.ID {
		case ix.ID:
			original = p.Interaction
		case approvalMirrorID(ix.ID):
			mirror = p.Interaction
		}
	}
	if !original.MirrorMain || original.MirrorOf != "" || mirror.MirrorOf != ix.ID || mirror.MirrorMain {
		t.Fatalf("Codex original/copy identity: original=%+v mirror=%+v", original, mirror)
	}
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = new(false) })
	if got := ft.s.pendingDisplayInteractions(context.Background()); len(got) != 2 {
		t.Fatalf("setting off withdrew an existing Codex copy from reconnects: %+v", got)
	}
}

func TestFeishuAssistantApprovalUsesDirectChatWithoutAllCards(t *testing.T) {
	ft := newMirrorFleetTest(t)
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = new(false) }) // bot routing has its own existing switch
	ft.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_a", PairedAt: time.Unix(100, 0),
			Features: FeishuFeatures{Main: true, Cards: true, AllCards: false}}, nil
	})
	ft.f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		return map[string]any{"message_id": "om_approval"}, nil
	})
	ctx := context.Background()
	ix := mirrorTestCard("ix_direct")
	if !ft.s.feishuCardWanted(ctx, ix) {
		t.Fatal("cards on with all_cards off did not admit the assistant approval")
	}
	ft.s.feishuInteraction(ix)
	posts := ft.f.appParams(AppFeishu, FeishuPost)
	if len(posts) != 1 {
		t.Fatalf("direct bot posts = %d, want 1", len(posts))
	}
	if posts[0]["chat_id"] != nil || posts[0]["in_thread"] != nil {
		t.Fatalf("approval was redirected away from the owner's direct chat: %+v", posts[0])
	}
	agent := mirrorTestCard("ix_agent")
	agent.SessionID, agent.Cwd = "agent", "/work/example"
	if ft.s.feishuCardWanted(ctx, agent) {
		t.Fatal("an unrelated Agent card was admitted with all_cards off")
	}
	ft.s.feishuInteraction(agent)
	private := mirrorTestCard("ix_private")
	private.Private = true
	if ft.s.feishuCardWanted(ctx, private) {
		t.Fatal("a private assistant card crossed the channel boundary")
	}
	ft.s.feishuInteraction(private)
	if got := len(ft.f.appParams(AppFeishu, FeishuPost)); got != 1 {
		t.Fatalf("excluded cards produced %d additional posts", got-1)
	}
}
