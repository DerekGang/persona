package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

type fakeBotmuxTurnsServer struct {
	mu           sync.Mutex
	token        string
	turnsStatus  int
	turnsErrCode string
	caps         []string
	turns        []transcript.BotmuxTurn
	echoActor    bool
	turnsGets    int
	triggers     []map[string]any
	srv          *httptest.Server
}

func newFakeBotmuxTurnsServer(t *testing.T) *fakeBotmuxTurnsServer {
	t.Helper()
	fs := &fakeBotmuxTurnsServer{token: "tok1", turnsStatus: http.StatusOK, echoActor: true}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if c, err := r.Cookie("botmux_dashboard_token"); err != nil || c.Value != fs.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/turns") {
			fs.turnsGets++
			w.Header().Set("Content-Type", "application/json")
			if fs.turnsStatus != http.StatusOK {
				w.WriteHeader(fs.turnsStatus)
				if fs.turnsErrCode != "" {
					fmt.Fprintf(w, `{"ok":false,"error":%q}`, fs.turnsErrCode)
				} else {
					fmt.Fprint(w, `{"ok":false,"error":"not_found"}`)
				}
				return
			}
			since := int64(0)
			fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
			var page []transcript.BotmuxTurn
			lastSeq := since
			for _, turn := range fs.turns {
				if turn.Seq > since {
					page = append(page, turn)
					if turn.Seq > lastSeq {
						lastSeq = turn.Seq
					}
				}
			}
			_ = json.NewEncoder(w).Encode(transcript.BotmuxTurnsResponse{
				OK:           true,
				Schema:       transcript.BotmuxTurnsSchema,
				Capabilities: fs.caps,
				Next:         fmt.Sprintf("%d", lastSeq),
				Turns:        page,
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/trigger" {
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			fs.triggers = append(fs.triggers, body)
			resp := map[string]any{"ok": true, "action": "delivered"}
			if fs.echoActor && body["actor"] == "owner" {
				resp["actor"] = "owner"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeBotmuxTurnsServer) makeHost(bin string) *botmuxHost {
	port := fs.srv.URL[strings.LastIndex(fs.srv.URL, ":")+1:]
	return &botmuxHost{
		bin:    bin,
		client: fs.srv.Client(),
		run: func(_ context.Context, args ...string) ([]byte, error) {
			fs.mu.Lock()
			tok := fs.token
			fs.mu.Unlock()
			switch strings.Join(args, " ") {
			case "observe --include-raw":
				return json.Marshal(map[string]any{
					"schemaVersion": 1,
					"daemons": []any{map[string]any{
						"larkAppId": bmxApp,
						"probe":     map[string]any{"status": "ok"},
						"sessions":  []any{bmxRow(sidA, "claude-code", "alive", "idle", false, "topic")},
					}},
				})
			case "dashboard current", "dashboard current " + botmuxShowLink:
				return []byte(bmxOldDashboard(port, tok)), nil
			default:
				return nil, fmt.Errorf("unexpected args: %v", args)
			}
		},
	}
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBotmuxVersionProbeAndOwnerActorSend(t *testing.T) {
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "botmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	fs := newFakeBotmuxTurnsServer(t)
	h := fs.makeHost(bin)

	// 1) Old BotMux (404): cached per instance, sends without actor, advertises external-trigger notice.
	fs.turnsStatus = http.StatusNotFound
	if err := h.Prompt(ctx, host.PaneRef{Kind: host.Botmux, ID: sidA}, "hello old"); err != nil {
		t.Fatalf("old botmux prompt: %v", err)
	}
	if err := h.Prompt(ctx, host.PaneRef{Kind: host.Botmux, ID: sidA}, "hello old 2"); err != nil {
		t.Fatalf("old botmux prompt 2: %v", err)
	}
	if fs.turnsGets != 1 {
		t.Fatalf("expected 1 cached probe GET for old botmux, got %d", fs.turnsGets)
	}
	if !h.externalTriggerNotice() {
		t.Fatal("expected externalTriggerNotice() == true on 404 old botmux")
	}
	if len(fs.triggers) != 2 || fs.triggers[0]["actor"] != nil || fs.triggers[1]["actor"] != nil {
		t.Fatalf("old botmux triggers must not carry actor: %+v", fs.triggers)
	}

	// 2) BotMux upgraded (binary changes): cache invalidates, re-probes 200 with ownerActor.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n# upgraded\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs.mu.Lock()
	fs.turnsStatus = http.StatusOK
	fs.caps = []string{transcript.BotmuxCapOwnerActor}
	fs.mu.Unlock()

	if err := h.Prompt(ctx, host.PaneRef{Kind: host.Botmux, ID: sidA}, "hello owner"); err != nil {
		t.Fatalf("new botmux prompt: %v", err)
	}
	if fs.turnsGets != 2 {
		t.Fatalf("expected re-probe after upgrade, turnsGets = %d", fs.turnsGets)
	}
	if h.externalTriggerNotice() {
		t.Fatal("expected externalTriggerNotice() == false when ownerActor is supported")
	}
	last := fs.triggers[len(fs.triggers)-1]
	if last["actor"] != "owner" {
		t.Fatalf("expected actor=owner, got %+v", last)
	}
	env, _ := last["envelope"].(map[string]any)
	if _, hasPayload := env["payload"]; hasPayload {
		t.Fatalf("owner actor envelope must not carry payload: %+v", env)
	}
	if _, hasRaw := env["rawText"]; hasRaw {
		t.Fatalf("owner actor envelope must not carry rawText: %+v", env)
	}

	// 3) BotMux restarted with rotated token and capabilities: [] (no ownerActor) -> re-probes, omits actor.
	fs.mu.Lock()
	fs.token = "tok2"
	fs.caps = []string{}
	fs.mu.Unlock()
	if err := h.Prompt(ctx, host.PaneRef{Kind: host.Botmux, ID: sidA}, "hello no-actor"); err != nil {
		t.Fatalf("no-actor prompt: %v", err)
	}
	if !h.externalTriggerNotice() {
		t.Fatal("expected externalTriggerNotice() == true when capabilities omits ownerActor")
	}
	if got := fs.triggers[len(fs.triggers)-1]["actor"]; got != nil {
		t.Fatalf("expected no actor when capability absent, got %v", got)
	}
}

func TestBotmuxOwnerActorUnechoedDoesNotResend(t *testing.T) {
	ctx := context.Background()
	fs := newFakeBotmuxTurnsServer(t)
	fs.caps = []string{transcript.BotmuxCapOwnerActor}
	fs.echoActor = false // simulates an instance that ignores actor but still runs the turn
	h := fs.makeHost("")

	if err := h.Prompt(ctx, host.PaneRef{Kind: host.Botmux, ID: sidA}, "once only"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if len(fs.triggers) != 1 {
		t.Fatalf("must never resend on unechoed actor; got %d triggers", len(fs.triggers))
	}
	if !h.externalTriggerNotice() {
		t.Fatal("unechoed actor must mark ownerActor unavailable for future sends")
	}
}

func TestBotmuxUnknownSession404DoesNotPoisonCache(t *testing.T) {
	ctx := context.Background()
	fs := newFakeBotmuxTurnsServer(t)
	fs.turnsStatus = http.StatusNotFound
	fs.turnsErrCode = "unknown_session"
	h := fs.makeHost("")

	caps := h.probeCaps(ctx, sidA)
	if caps.known {
		t.Fatalf("404 unknown_session must not be cached as old botmux: %+v", caps)
	}
	if h.externalTriggerNotice() {
		t.Fatal("unknown_session must not enable externalTriggerNotice")
	}
}

func TestBotmuxStructuredReadIncrementalAndFold(t *testing.T) {
	ctx := context.Background()
	fs := newFakeBotmuxTurnsServer(t)
	fs.caps = []string{transcript.BotmuxCapOwnerActor}

	rawOld := "<botmux_reminder>reply</botmux_reminder>\n\n<user_message>\nold turn\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_old\" name=\"Sam\" />"
	rawNew := "<botmux_reminder>reply</botmux_reminder>\n\n<user_message>\nnew turn\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_owner\" name=\"Alex\" />"

	fs.turns = []transcript.BotmuxTurn{
		{
			TurnID: "t_new",
			Seq:    5,
			At:     1763520000000,
			Inbound: []transcript.BotmuxTurnInbound{{
				MessageID: "om_1",
				Text:      "new turn",
				Sender:    &transcript.BotmuxTurnSender{Type: "user", OpenID: "ou_owner", Name: "Alex"},
				Via:       "owner_actor",
				Placement: &transcript.BotmuxTurnPlacement{Kind: "thread", RootMessageID: "om_root"},
			}},
			Replies: []transcript.BotmuxTurnReply{{
				MessageID:    "om_r1",
				ResponseKind: "final",
				At:           1763520010000,
				Text:         "Card reply full text",
			}},
			Anchor: &transcript.BotmuxTurnAnchor{
				CLIID:       "claude-code",
				InputSha256: sha256Of(rawNew),
			},
		},
	}

	h := fs.makeHost("")
	n, _, home := herdrNode(t)
	n.hosts = host.NewRegistry(h)
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatal(err)
	}

	// Write a real Claude transcript with an old turn (no structured record) + a new turn (with --card-file reply).
	p := filepath.Join(home, ".claude", "projects", "proj", sidA+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":"2026-11-19T01:00:00Z","message":{"role":"user","content":%q}}`, rawOld),
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-19T01:00:01Z","message":{"role":"assistant","id":"m1","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"botmux send 'old reply'"}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-19T01:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"ok"}]}}`,
		fmt.Sprintf(`{"type":"user","uuid":"u2","timestamp":"2026-11-19T01:01:00Z","message":{"role":"user","content":%q}}`, rawNew),
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-19T01:01:01Z","message":{"role":"assistant","id":"m2","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","uuid":"r2","timestamp":"2026-11-19T01:01:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":"ok"}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-11-19T01:01:03Z","message":{"role":"assistant","id":"m3","content":[{"type":"tool_use","id":"tu3","name":"Bash","input":{"command":"botmux send --card-file /tmp/card.json"}}]}}`,
		`{"type":"user","uuid":"r3","timestamp":"2026-11-19T01:01:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu3","content":"ok"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rawResp, err := n.handle(ctx, "session.items", bmxMustJSON(map[string]any{"session_id": sidA, "fold_work": true, "limit": 50}))
	if err != nil {
		t.Fatalf("session.items: %v", err)
	}
	var page struct {
		Items []proto.Item `json:"items"`
	}
	b, _ := json.Marshal(rawResp)
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	var oldUser, newUser *proto.Botmux
	var foldedReplies []proto.Botmux
	for _, it := range page.Items {
		if it.ID == "u1#0" {
			oldUser = it.Botmux
		}
		if it.ID == "u2#0" {
			newUser = it.Botmux
		}
		if it.Kind == proto.KindFold && it.Fold != nil {
			foldedReplies = append(foldedReplies, it.Fold.Botmux...)
		}
	}
	if oldUser == nil || oldUser.Text != "old turn" || oldUser.Sender != "Sam" {
		t.Fatalf("old turn should fall back to wrapper parsing, got %+v", oldUser)
	}
	if newUser == nil || newUser.Text != "new turn" || newUser.Sender != "Alex" || !newUser.Thread {
		t.Fatalf("new turn should use structured turn record, got %+v", newUser)
	}
	if len(foldedReplies) != 1 || foldedReplies[0].Text != "Card reply full text" || !foldedReplies[0].Final {
		t.Fatalf("folded --card-file reply should carry structured text, got %+v", foldedReplies)
	}

	// Late reply arrives with higher seq: incremental fetch updates the cached turn without re-fetching from 0.
	fs.mu.Lock()
	fs.turns[0].Seq = 9
	fs.turns[0].Replies[0].Text = "Updated card reply"
	fs.mu.Unlock()
	rawResp2, err := n.handle(ctx, "session.items", bmxMustJSON(map[string]any{"session_id": sidA, "fold_work": true, "limit": 50}))
	if err != nil {
		t.Fatalf("session.items 2: %v", err)
	}
	var page2 struct {
		Items []proto.Item `json:"items"`
	}
	b2, _ := json.Marshal(rawResp2)
	_ = json.Unmarshal(b2, &page2)
	var gotUpdated string
	for _, it := range page2.Items {
		if it.Kind == proto.KindFold && it.Fold != nil && len(it.Fold.Botmux) > 0 {
			gotUpdated = it.Fold.Botmux[0].Text
		}
	}
	if gotUpdated != "Updated card reply" {
		t.Fatalf("expected updated late reply, got %q", gotUpdated)
	}
}

func bmxMustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestBotmuxListKeyIncludesExternalTrigger(t *testing.T) {
	a := []proto.Session{{SessionID: sidA, PaneID: "botmux:" + sidA, UpdatedAt: time.Unix(100, 0)}}
	b := []proto.Session{{SessionID: sidA, PaneID: "botmux:" + sidA, UpdatedAt: time.Unix(100, 0), BotmuxExternalTrigger: true}}
	if listKey(a) == listKey(b) {
		t.Fatal("listKey must change when BotmuxExternalTrigger toggles")
	}
}
