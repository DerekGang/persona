package transcript

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

const botmuxFixtures = "../../../spec/fixtures/botmux/cases.json"

func TestBotmuxFixtures(t *testing.T) {
	raw, err := os.ReadFile(botmuxFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Messages []struct {
			Name string        `json:"name"`
			Text string        `json:"text"`
			Want *proto.Botmux `json:"want"`
		} `json:"messages"`
		Sends []struct {
			Name    string        `json:"name"`
			Command string        `json:"command"`
			Want    *proto.Botmux `json:"want"`
		} `json:"sends"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Messages) == 0 || len(fx.Sends) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range fx.Messages {
		if got := BotmuxMessage(c.Text); !reflect.DeepEqual(got, c.Want) {
			t.Errorf("message %s:\n got %+v\nwant %+v", c.Name, got, c.Want)
		}
	}
	for _, c := range fx.Sends {
		if got := BotmuxSend(c.Command); !reflect.DeepEqual(got, c.Want) {
			t.Errorf("send %s:\n got %+v\nwant %+v", c.Name, got, c.Want)
		}
	}
}

// A BotMux session's transcript: the user line and the `botmux send` call carry what they say, the text stays the
// original, and a folded run keeps its replies.
func TestBotmuxParseAndFold(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-19T01:00:00Z","message":{"role":"user","content":"<botmux_reminder>reply with botmux send</botmux_reminder>\n\n<user_message>\nship it\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_example_alex\" name=\"Alex Chen\" />"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-19T01:00:01Z","message":{"role":"assistant","id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-19T01:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-19T01:00:03Z","message":{"role":"assistant","id":"m2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"botmux send --response-kind final 'shipped'"}}]}}`,
		`{"type":"user","uuid":"r2","timestamp":"2026-11-19T01:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"{\"ok\":true}"}]}}`,
	}
	var meta Meta
	var items []proto.Item
	for i, l := range lines {
		items = append(items, ParseLine(i+1, []byte(l), &meta)...)
	}
	if len(items) != 5 {
		t.Fatalf("items = %d, want 5: %+v", len(items), items)
	}
	user := items[0]
	if user.Botmux == nil || user.Botmux.Text != "ship it" || user.Botmux.Sender != "Alex Chen" {
		t.Fatalf("user botmux = %+v", user.Botmux)
	}
	if user.Text == "ship it" {
		t.Fatal("the item's text must stay the original")
	}
	if items[1].Botmux != nil {
		t.Fatalf("a plain Bash call is not a reply: %+v", items[1].Botmux)
	}
	if r := items[3].Botmux; r == nil || r.Kind != proto.BotmuxReply || r.Text != "shipped" || !r.Final {
		t.Fatalf("reply botmux = %+v", r)
	}
	folded := FoldWork(items)
	if len(folded) != 2 || folded[1].Fold == nil {
		t.Fatalf("folded = %+v", folded)
	}
	if got := folded[1].Fold.Botmux; len(got) != 1 || got[0].Text != "shipped" {
		t.Fatalf("fold botmux = %+v", got)
	}
}

func TestBotmuxTurnsFixtures(t *testing.T) {
	raw, err := os.ReadFile(botmuxFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		TurnsCases []struct {
			Name           string               `json:"name"`
			Status         int                  `json:"status"`
			Response       *BotmuxTurnsResponse `json:"response"`
			WantOwnerActor bool                 `json:"want_owner_actor"`
			Items          []proto.Item         `json:"items"`
			WantBotmux     []*proto.Botmux      `json:"want_botmux"`
		} `json:"turns_cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.TurnsCases) == 0 {
		t.Fatal("no turns_cases")
	}
	for _, tc := range fx.TurnsCases {
		t.Run(tc.Name, func(t *testing.T) {
			gotOwner := tc.Status == 200 && tc.Response != nil && tc.Response.HasCapability(BotmuxCapOwnerActor)
			if gotOwner != tc.WantOwnerActor {
				t.Fatalf("ownerActor = %v, want %v", gotOwner, tc.WantOwnerActor)
			}
			// Simulate ParseLine's wrapper fallback first, then overlay structured turns when available.
			items := append([]proto.Item(nil), tc.Items...)
			origTexts := make([]string, len(items))
			for i := range items {
				origTexts[i] = items[i].Text
			}
			markBotmux(items)
			if tc.Status == 200 && tc.Response != nil && tc.Response.OK && tc.Response.Schema == BotmuxTurnsSchema {
				items = ApplyBotmuxTurns(items, tc.Response.Turns)
			}
			if len(items) != len(tc.WantBotmux) {
				t.Fatalf("items = %d, want %d", len(items), len(tc.WantBotmux))
			}
			for i, want := range tc.WantBotmux {
				if items[i].Text != origTexts[i] {
					t.Errorf("item[%d].Text mutated", i)
				}
				if !reflect.DeepEqual(items[i].Botmux, want) {
					t.Errorf("item[%d].Botmux:\n got %+v\nwant %+v", i, items[i].Botmux, want)
				}
				if items[i].Botmux != nil {
					b, _ := json.Marshal(items[i].Botmux)
					if s := string(b); containsAny(s, "ou_example_", "om_example_") {
						t.Errorf("item[%d].Botmux leaked Lark ID: %s", i, s)
					}
				}
			}
		})
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
