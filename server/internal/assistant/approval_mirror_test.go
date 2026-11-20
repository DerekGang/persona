package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
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
		case approvalMirrorID(ix):
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

func TestApprovalMirrorRoutesOnlyWriteTools(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ctx := context.Background()
	ft.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_a", PairedAt: time.Unix(100, 0),
			Features: FeishuFeatures{Main: true, Cards: true, AllCards: false}}, nil
	})
	for _, name := range []string{"Read", "lark_messages", "mcp__persona__lark_messages", "kb_get", "inbox_suggest"} {
		t.Run("read "+name, func(t *testing.T) {
			ix := mirrorTestCard("ix_read_" + name)
			ix.ToolName = name
			if ft.s.nonMainAssistantPermission(ctx, ix) || ft.s.feishuCardWanted(ctx, ix) {
				t.Fatalf("read-only tool %q entered a new route", name)
			}
			if _, ok := ft.s.mainApprovalMirror(ctx, ix); ok {
				t.Fatalf("read-only tool %q gained a main copy", name)
			}
			// Narrowing new routing does not strand a copy that already exists.
			ix.MirrorMain = true
			if _, ok := ft.s.mainApprovalMirror(ctx, ix); !ok {
				t.Fatalf("existing copy for %q was withdrawn", name)
			}
		})
	}
	for _, tc := range []struct{ name, input string }{
		{"Bash", `{"command":"mkdir -p /tmp/example"}`},
		{"persona_settings", `{"action":"set","key":"assistant.main_tasks_quiet","value":true}`},
		{"schedule_routine", `{"title":"Review","time":"18:00"}`},
		{"todo_complete", `{"id":"todo_example"}`},
		{"mcp__persona__mail_send", `{"to":"me@example.com","subject":"Review"}`},
	} {
		t.Run("write "+tc.name, func(t *testing.T) {
			ix := mirrorTestCard("ix_write_" + tc.name)
			ix.ToolName, ix.ToolInput = tc.name, json.RawMessage(tc.input)
			if !ft.s.nonMainAssistantPermission(ctx, ix) || !ft.s.feishuCardWanted(ctx, ix) {
				t.Fatalf("write tool %q missed a new route", tc.name)
			}
			if _, ok := ft.s.mainApprovalMirror(ctx, ix); !ok {
				t.Fatalf("write tool %q has no main copy", tc.name)
			}
		})
	}
	gated := mirrorTestCard("ix_gated_external_write")
	gated.ToolName = "Configured external action"
	gated.AlwaysKeys = []string{"lark:POST /messages"}
	if !ft.s.nonMainAssistantPermission(ctx, gated) || !ft.s.feishuCardWanted(ctx, gated) {
		t.Fatal("an existing command-gate external write route was not admitted")
	}
}

func TestApprovalMirrorToolAllowlistIsConfigurable(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ctx := context.Background()
	ft.s.SetExtensions(&guestExt{})
	for _, tc := range []struct {
		tool string
		want bool
	}{{"lark_chats", false}, {"lark_agent_reply", true}, {"computer_run", true}} {
		ix := mirrorTestCard("ix_ext_" + tc.tool)
		ix.ToolName = tc.tool
		if got := ft.s.nonMainAssistantPermission(ctx, ix); got != tc.want {
			t.Fatalf("extension tool %s routed=%v, want %v", tc.tool, got, tc.want)
		}
	}
	configured := []string{" Read ", "Read", ""}
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorTools = &configured })
	if got := ft.s.view(ctx, false).ApprovalMirrorTools; len(got) != 1 || got[0] != "Read" {
		t.Fatalf("effective custom allowlist = %q, want Read", got)
	}
	read := mirrorTestCard("ix_custom_read")
	read.ToolName = "Read"
	write := mirrorTestCard("ix_custom_bash")
	if !ft.s.nonMainAssistantPermission(ctx, read) || ft.s.nonMainAssistantPermission(ctx, write) {
		t.Fatal("custom allowlist did not replace the default and extension fallback")
	}
	empty := []string{}
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorTools = &empty })
	if ft.s.nonMainAssistantPermission(ctx, read) || len(ft.s.view(ctx, false).ApprovalMirrorTools) != 0 {
		t.Fatal("an explicitly empty allowlist must disable new copies")
	}
}

func TestApprovalMirrorIDNamespaceIsReservedForNodes(t *testing.T) {
	ix := mirrorTestCard("ix_reserved")
	ix.ID = approvalMirrorID(proto.Interaction{ID: "ix_some_other_approval"})
	if !reservedCard(ix) {
		t.Fatal("a node-controlled card entered the display-copy ID namespace")
	}
	if reservedCard(mirrorTestCard("ix_authoritative")) {
		t.Fatal("an ordinary authoritative ID was rejected")
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
		case approvalMirrorID(ix):
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

func TestCodexApprovalMirrorRestoresOnlyTheSamePendingOccurrence(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ix := mirrorTestCard("ix_codex_restart")
	ix.Resumable = false // production Codex dialogs are node-listed, not hook-resumable
	ix.CreatedAt = time.Unix(1000, 0).UTC()
	listCodex(ft.s, ix)
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = new(false) })
	restarted := func() *Service {
		f := newFakeHub()
		f.mu.Lock()
		f.hello.ApprovalMirrors = true
		f.mu.Unlock()
		return newRestartedService(t, Options{Hub: f, Store: func() Store { return ft.st }, Logf: t.Logf, Devices: ft.d})
	}

	// A node relist before the restored in-memory map arrives must use the
	// durable occurrence, not reconstruct intent from the new preference.
	same := restarted()
	listCodex(same, ix)
	pending := same.pendingInteractions()
	if len(pending) != 1 || !pending[0].MirrorMain || len(same.pendingDisplayInteractions(context.Background())) != 2 {
		t.Fatalf("same pending occurrence lost its mirror after restart: %+v", pending)
	}

	changed := ix
	changed.CreatedAt = changed.CreatedAt.Add(time.Second)
	different := restarted()
	listCodex(different, changed)
	if pending := different.pendingInteractions(); len(pending) != 1 || pending[0].MirrorMain {
		t.Fatalf("a new occurrence inherited the old occurrence's mirror: %+v", pending)
	}

	// Startup cleanup may cancel a non-resumable row before the still-open node
	// dialog is listed again. Its unchanged occurrence keeps the recorded intent.
	if err := ft.st.FinishInteraction(context.Background(), ix.ID, "cancelled", nil); err != nil {
		t.Fatal(err)
	}
	afterCleanup := restarted()
	listCodex(afterCleanup, ix)
	if pending := afterCleanup.pendingInteractions(); len(pending) != 1 || !pending[0].MirrorMain || len(afterCleanup.pendingDisplayInteractions(context.Background())) != 2 {
		t.Fatalf("the open dialog lost its mirror after startup cleanup: %+v", pending)
	}
	ft.st.fmu.Lock()
	ft.st.status[ix.ID] = "pending"
	for i := range ft.st.recs {
		if ft.st.recs[i].Interaction.ID == ix.ID {
			ft.st.recs[i].Status = "pending"
		}
	}
	ft.st.fmu.Unlock()

	// The inverse is also occurrence state: an old unmirrored card must not
	// become mirrored merely because the current default is on.
	ft.st.fmu.Lock()
	for i := range ft.st.recs {
		if ft.st.recs[i].Interaction.ID == ix.ID {
			ft.st.recs[i].Interaction.MirrorMain = false
		}
	}
	ft.st.fmu.Unlock()
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = nil })
	unmirrored := restarted()
	listCodex(unmirrored, ix)
	if pending := unmirrored.pendingInteractions(); len(pending) != 1 || pending[0].MirrorMain {
		t.Fatalf("an old unmirrored occurrence gained a copy from the current setting: %+v", pending)
	}
}

func TestHookApprovalMirrorRestoresOnlyTheSamePendingOccurrence(t *testing.T) {
	for _, tc := range []struct {
		name           string
		recorded       bool
		currentDefault *bool
	}{
		{name: "mirrored occurrence survives setting off", recorded: true, currentDefault: new(false)},
		{name: "unmirrored occurrence survives setting on", recorded: false, currentDefault: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ft := newMirrorFleetTest(t)
			setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = tc.currentDefault })
			ix := mirrorTestCard("ix_hook_restart_" + map[bool]string{true: "mirrored", false: "unmirrored"}[tc.recorded])
			ix.CreatedAt = time.Unix(1000, 0).UTC()
			recorded := ix
			recorded.MirrorMain = tc.recorded
			ft.st.fmu.Lock()
			ft.st.recs = append(ft.st.recs, db.InteractionRecord{Interaction: recorded, Status: "pending"})
			ft.st.status[ix.ID] = "pending"
			ft.st.fmu.Unlock()
			f := newFakeHub()
			f.mu.Lock()
			f.hello.ApprovalMirrors = true
			f.mu.Unlock()
			restarted := newRestartedService(t, Options{Hub: f, Store: func() Store { return ft.st }, Logf: t.Logf, Devices: ft.d})
			done := make(chan error, 1)
			go func() {
				_, err := restarted.waitInteraction(context.Background(), ix)
				done <- err
			}()
			waitFor(t, "resumable hook reclaim", func() bool { return len(restarted.pendingInteractions()) == 1 })
			pending := restarted.pendingInteractions()
			if len(pending) != 1 || pending[0].MirrorMain != tc.recorded {
				t.Fatalf("reclaimed occurrence intent = %+v, want mirror_main=%v", pending, tc.recorded)
			}
			wantCards := 1
			if tc.recorded {
				wantCards = 2
			}
			if got := restarted.pendingDisplayInteractions(context.Background()); len(got) != wantCards {
				t.Fatalf("reclaimed display cards = %+v, want %d", got, wantCards)
			}
			yes := true
			if err := restarted.resolve(ix.ID, proto.Resolution{Allow: &yes}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reclaimed hook did not finish")
			}
		})
	}
}

func TestDetachedReplayRestoresRecordedMirrorIntent(t *testing.T) {
	for _, tc := range []struct {
		name           string
		recorded       bool
		currentDefault *bool
	}{
		{name: "mirrored original survives setting off", recorded: true, currentDefault: new(false)},
		{name: "unmirrored original survives setting on", recorded: false, currentDefault: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ft := newMirrorFleetTest(t)
			setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = tc.currentDefault })
			ix := detachedTestCard("ix_detached_mirror_" + map[bool]string{true: "mirrored", false: "unmirrored"}[tc.recorded])
			ix.SessionID = "side"
			ix.CreatedAt = time.Unix(1000, 0).UTC()
			recorded := ix
			recorded.MirrorMain = tc.recorded
			ft.st.fmu.Lock()
			ft.st.recs = append(ft.st.recs, db.InteractionRecord{Interaction: recorded, Status: "pending"})
			ft.st.status[ix.ID] = "pending"
			ft.st.fmu.Unlock()

			restarted := newRestartedService(t, Options{Hub: ft.f, Store: func() Store { return ft.st }, Logf: t.Logf, Devices: ft.d})
			replay := ix
			replay.CreatedAt = replay.CreatedAt.Add(time.Second) // a detachable original replay may refresh its timestamp
			done := make(chan detachedTestResult, 1)
			go func() {
				res, err := restarted.nodeInteraction(context.Background(), replay)
				done <- detachedTestResult{res: res, err: err}
			}()
			waitFor(t, "detachable original replay", func() bool { return len(restarted.pendingInteractions()) == 1 })
			pending := restarted.pendingInteractions()
			if len(pending) != 1 || pending[0].MirrorMain != tc.recorded {
				t.Fatalf("replayed display intent = %+v, want mirror_main=%v", pending, tc.recorded)
			}
			wantCards := 1
			if tc.recorded {
				wantCards = 2
			}
			if got := restarted.pendingDisplayInteractions(context.Background()); len(got) != wantCards {
				t.Fatalf("replayed display cards = %+v, want %d", got, wantCards)
			}
			deny := false
			if err := restarted.resolve(ix.ID, proto.Resolution{Allow: &deny}); err != nil {
				t.Fatal(err)
			}
			if got := awaitDetachedTest(t, done); got.err != nil || (got.res.Allow != nil && *got.res.Allow) {
				t.Fatalf("denied replay = %+v, %v", got.res, got.err)
			}
		})
	}
}

func TestJustMeCodexApprovalHasNoMainOrDirectChatCopy(t *testing.T) {
	ft := newMirrorFleetTest(t)
	ft.s.justMeSent("mac", "side")
	ft.s.justMeStarted("mac", "side", true, false)
	ft.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_a", PairedAt: time.Unix(100, 0),
			Features: FeishuFeatures{Main: true, Cards: true, AllCards: false}}, nil
	})
	ft.f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		return map[string]any{"message_id": "om_private"}, nil
	})
	ix := mirrorTestCard("ix_just_me_codex")
	listCodex(ft.s, ix)
	pending := ft.s.pendingInteractions()
	if len(pending) != 1 || !pending[0].Private {
		t.Fatalf("Just me Codex card = %+v, want one private original", pending)
	}
	if got := ft.s.pendingDisplayInteractions(context.Background()); len(got) != 1 {
		t.Fatalf("Just me display cards = %+v, want no main copy", got)
	}
	if posts := ft.f.appParams(AppFeishu, FeishuPost); len(posts) != 0 {
		t.Fatalf("Just me approval reached the direct bot chat: %+v", posts)
	}
}

func TestFeishuAssistantApprovalWithTopicRunUsesDirectChat(t *testing.T) {
	ft := newMirrorFleetTest(t)
	setSettings(t, ft.s, func(v *Settings) { v.ApprovalMirrorMain = new(false) })
	ft.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_a", PairedAt: time.Unix(100, 0),
			Features: FeishuFeatures{Main: true, Cards: true, AllCards: false}}, nil
	})
	ft.f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		return map[string]any{"message_id": "om_approval"}, nil
	})
	ft.f.answerApp(AppFeishu, FeishuText, func(json.RawMessage) (any, error) {
		return map[string]any{"message_id": "om_handoff"}, nil
	})
	c := &ft.s.chans
	c.mu.Lock()
	c.st.Runs = append(c.st.Runs, &channel.Run{ID: "run_topic", Channel: channel.Feishu,
		MachineID: "mac", SessionID: "side", Started: time.Now(), ReplyTo: "om_topic",
		Ext: map[string]string{"chat": "oc_topic", "thread": "omt_topic"}})
	c.mu.Unlock()
	ft.s.feishuInteraction(mirrorTestCard("ix_direct_topic"))
	posts := ft.f.appParams(AppFeishu, FeishuPost)
	if len(posts) != 1 {
		t.Fatalf("direct bot posts = %d, want 1", len(posts))
	}
	replyTo, _ := posts[0]["reply_to"].(string)
	if posts[0]["chat_id"] != nil || posts[0]["in_thread"] != nil || replyTo != "" {
		t.Fatalf("approval went to its topic run instead of a new direct-chat card: %+v", posts[0])
	}
	notes := topicNotes(ft.f)
	line := i18n.T(ft.s.botLang(context.Background()), "assistant-feishu_cards.sent_to_dm")
	if len(notes) != 1 || notes[0]["text"] != line || notes[0]["reply_to"] != "om_topic" ||
		notes[0]["chat_id"] != "oc_topic" || notes[0]["in_thread"] != true {
		t.Fatalf("topic did not receive its existing direct-chat handoff notice: %+v", notes)
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
