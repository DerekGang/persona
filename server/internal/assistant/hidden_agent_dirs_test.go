package assistant

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestHiddenDirectoryMatching(t *testing.T) {
	for _, tc := range []struct {
		cwd, home, pattern string
		want               bool
	}{
		{"/home/user/.automation/work/a", "/home/user", "~/.automation", true},
		{"/home/user/.automation-copy", "/home/user", "~/.automation", false},
		{"/home/other/.automation", "/home/user", "~/.automation", false},
		{"/home/user/.automation", "", "~/.automation", false},
		{"~/work/a", "/home/user", "~/work", true},
		{"/work/team/a/src", "", "/work/*/a", true},
		{"/work/a", "", "/work/**/a", true},
		{"/work/team/deep/a/src", "", "/work/**/a", true},
		{"/work/team/deep/b", "", "/work/**/a", false},
		{"/work/a/../b", "", "/work/a", false},
		{`C:\Users\user\work\a`, `C:\Users\user`, "~/work", true},
		{"", "/home/user", "~", false},
	} {
		if got := directoryMatches(tc.cwd, tc.home, tc.pattern); got != tc.want {
			t.Errorf("%q home %q pattern %q: %v", tc.cwd, tc.home, tc.pattern, got)
		}
	}
}

func TestHiddenDirectoryRulesRoutesAndPersistence(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	endpoint := "/api/v1/assistant/hidden-agent-dirs"
	if w := serve(s, "GET", endpoint, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"rules":[]`) {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}
	body := `{"rules":[{"machine_id":"box","pattern":"/work/*","enabled":true}]}`
	if w := serve(s, "PUT", endpoint, body); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	for _, agent := range []string{"claude", "codex", "pi"} {
		c := proto.Session{SessionID: "ambient", Cwd: "/work/a", Agent: agent, StartedByPersona: true}
		if !s.hiddenByDirectory("box", c) || s.hiddenByDirectory("other", c) {
			t.Fatalf("runtime/machine policy: %s", agent)
		}
	}
	again := New(Options{Store: s.store})
	if v, err := again.readHiddenDirs(context.Background()); err != nil || len(v.Rules) != 1 {
		t.Fatalf("restart: %+v %v", v, err)
	}
	r := httptest.NewRequest("PUT", endpoint, strings.NewReader(`{"rules":[]}`))
	r.Header.Set(RoleHeader, "answer")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("read-only write: %d", w.Code)
	}
	for _, bad := range []string{`{"rules":[{"machine_id":"box","pattern":"relative","enabled":true}]}`, `{"rules":[{"machine_id":"box","pattern":"/work/[","enabled":true}]}`} {
		if w := serve(s, "PUT", endpoint, bad); w.Code != 400 {
			t.Fatalf("invalid: %d %s", w.Code, w.Body)
		}
	}
	if w := serve(s, "PUT", endpoint, strings.Replace(body, `"enabled":true`, `"enabled":false`, 1)); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if s.hiddenByDirectory("box", proto.Session{Cwd: "/work/a"}) {
		t.Fatal("disabled rule still hides")
	}
}

func TestHiddenDirectorySettingConfirmation(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "declined", true: "approved"}[allow], func(t *testing.T) {
			s := newTestService(t, newFakeHub().withMachine())
			go func() {
				q, ok := awaitCard(s)
				if !ok {
					t.Error("missing phone confirmation")
					return
				}
				raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
				if _, err := s.Action(context.Background(), Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
					t.Error(err)
				}
			}()
			result := s.Tool(context.Background(), ToolCall{Name: "persona_settings", MachineID: "server", SessionID: "c1", Arguments: json.RawMessage(`{"action":"set","key":"agents.hidden_dirs","value":{"rules":[{"machine_id":"box","pattern":"/work","enabled":true}]}}`)})
			if result.IsError {
				t.Fatalf("tool: %+v", result)
			}
			v, err := s.readHiddenDirs(context.Background())
			if err != nil || (len(v.Rules) == 1) != allow {
				t.Fatalf("approval=%v rules=%+v error=%v", allow, v, err)
			}
		})
	}
}

func TestHiddenDirectoryApprovalDoesNotOverwriteNewerRules(t *testing.T) {
	s := newTestService(t, newFakeHub())
	change, err := s.hiddenDirSetting().set(context.Background(), json.RawMessage(`{"rules":[{"machine_id":"box","pattern":"/work","enabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	v := hiddenAgentDirs{Rules: []hiddenAgentDir{{MachineID: "box", Pattern: "/other", Enabled: true}}}
	if err = s.saveHiddenDirs(context.Background(), v, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = change.apply(context.Background()); err == nil {
		t.Fatal("stale approval overwrote newer settings")
	}
}

func TestHiddenDirectoryDatabasePersistence(t *testing.T) {
	database := taskRecoveryDatabase(t)
	s := newServiceOn(t, newFakeHub().withMachine(), database)
	v := hiddenAgentDirs{Rules: []hiddenAgentDir{{MachineID: "box", Pattern: "/work", Enabled: true}}}
	if err := s.saveHiddenDirs(context.Background(), v, nil); err != nil {
		t.Fatal(err)
	}
	restarted := newServiceOn(t, newFakeHub().withMachine(), database)
	got, err := restarted.readHiddenDirs(context.Background())
	if err != nil || len(got.Rules) != 1 || got.Rules[0] != v.Rules[0] {
		t.Fatalf("Postgres restart: %+v %v", got, err)
	}
}
