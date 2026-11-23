package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// setAttentionClockForTest makes the attention mode read now instead of the wall clock.
func (s *Service) setAttentionClockForTest(now func() time.Time) {
	s.attention.mu.Lock()
	s.attention.now = now
	s.attention.cached = nil
	s.attention.mu.Unlock()
}

// attentionCall calls an attention route as role ("" is full) and decodes the answer.
func attentionCall(t *testing.T, s *Service, method, role, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/api/v1/assistant/attention", strings.NewReader(body))
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	s.ServeHTTP(w, r)
	var out map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func decodeAttention(t *testing.T, raw json.RawMessage) assistantwire.Attention {
	t.Helper()
	var a assistantwire.Attention
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("attention %s: %v", raw, err)
	}
	return a
}

func TestNewAttentionValidates(t *testing.T) {
	now := time.Date(2026, 11, 22, 21, 30, 0, 0, time.UTC)
	known := func(slug string) bool { return slug == "qa-plain" }
	for _, c := range []struct {
		name string
		set  assistantwire.AttentionSet
		ok   bool
	}{
		{"normal needs no minutes", assistantwire.AttentionSet{Mode: "normal"}, true},
		{"unknown mode", assistantwire.AttentionSet{Mode: "focus", Minutes: 30}, false},
		{"empty mode", assistantwire.AttentionSet{Minutes: 30}, false},
		{"a mode needs a time limit", assistantwire.AttentionSet{Mode: "quiet"}, false},
		{"a minute at least", assistantwire.AttentionSet{Mode: "fragment", Minutes: 0}, false},
		{"a day at most", assistantwire.AttentionSet{Mode: "quiet", Minutes: 24*60 + 1}, false},
		{"a whole day", assistantwire.AttentionSet{Mode: "quiet", Minutes: 24 * 60}, true},
		{"negative minutes", assistantwire.AttentionSet{Mode: "immersive", Minutes: -5}, false},
		{"a known track in focus", assistantwire.AttentionSet{Mode: "immersive", Minutes: 30, Focus: &assistantwire.AttentionFocus{Track: "qa-plain"}}, true},
		{"an unknown track in focus", assistantwire.AttentionSet{Mode: "immersive", Minutes: 30, Focus: &assistantwire.AttentionFocus{Track: "qa-gone"}}, false},
		{"a session in focus", assistantwire.AttentionSet{Mode: "immersive", Minutes: 30, Focus: &assistantwire.AttentionFocus{MachineID: "mac", SessionID: "s1"}}, true},
		{"a session id too long", assistantwire.AttentionSet{Mode: "immersive", Minutes: 30, Focus: &assistantwire.AttentionFocus{SessionID: strings.Repeat("x", 201)}}, false},
	} {
		a, ok := newAttention(c.set, assistantwire.AttentionByUser, now, known)
		if ok != c.ok {
			t.Errorf("%s: ok %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if a.SetBy != assistantwire.AttentionByUser || !a.SetAt.Equal(now) {
			t.Errorf("%s: set by %q at %v", c.name, a.SetBy, a.SetAt)
		}
		if a.Mode == assistantwire.AttentionNormal && !a.Until.IsZero() {
			t.Errorf("%s: normal has an end %v", c.name, a.Until)
		}
		if a.Mode != assistantwire.AttentionNormal && !a.Until.Equal(now.Add(time.Duration(c.set.Minutes)*time.Minute)) {
			t.Errorf("%s: ends %v", c.name, a.Until)
		}
	}
	// A focus is immersive mode's only.
	if a, ok := newAttention(assistantwire.AttentionSet{Mode: "quiet", Minutes: 30, Focus: &assistantwire.AttentionFocus{Track: "qa-plain"}},
		assistantwire.AttentionByUser, now, known); !ok || a.Focus != nil {
		t.Errorf("quiet with a focus: %+v %v", a, ok)
	}
}

func TestCappedLevel(t *testing.T) {
	for _, c := range []struct{ level, limit, want string }{
		{"", "", ""},
		{"", "P0", ""},
		{"P0", "P0", "P0"},
		{"P1", "P0", "P1"},
		{"", "P1", "P1"},
		{"P0", "P1", "P1"},
		{"P1", "P1", "P1"},
		{"P0", "P2", "P2"},
		{"P1", "P2", "P2"},
		{"", "P2", "P2"},
		{"P0", "bogus", "P0"},
	} {
		if got := cappedLevel(c.level, c.limit); got != c.want {
			t.Errorf("cappedLevel(%q, %q) = %q, want %q", c.level, c.limit, got, c.want)
		}
	}
}

// Every built-in track's cap is P0, so the health space's notify_user level is what it was.
func TestTrackPushLevelLeavesHealthAlone(t *testing.T) {
	ft := newFleetTest(t)
	ctx := context.Background()
	for _, level := range []string{"", "P0", "P1"} {
		if got := ft.s.trackPushLevel(ctx, "health", level); got != level {
			t.Errorf("health %q -> %q", level, got)
		}
		if got := ft.s.trackPushLevel(ctx, "", level); got != level {
			t.Errorf("main %q -> %q", level, got)
		}
	}
}

func TestAttentionRoutes(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	var mu sync.Mutex
	now := time.Date(2026, 11, 22, 21, 30, 0, 0, time.UTC)
	s.setAttentionClockForTest(func() time.Time { mu.Lock(); defer mu.Unlock(); return now })

	code, out := attentionCall(t, s, http.MethodGet, "", "")
	if code != http.StatusOK || decodeAttention(t, out["attention"]).Mode != assistantwire.AttentionNormal {
		t.Fatalf("before any: %d %s", code, out["attention"])
	}
	// Only the user sets it.
	for _, role := range []string{roleExtension, "agent", "scoped"} {
		if code, _ := attentionCall(t, s, http.MethodPut, role, `{"mode":"quiet","minutes":30}`); code != http.StatusForbidden {
			t.Errorf("%s PUT: %d", role, code)
		}
	}
	if code, _ := attentionCall(t, s, http.MethodPut, roleFull, `{"mode":"quiet"}`); code != http.StatusBadRequest {
		t.Errorf("no minutes: %d", code)
	}
	code, out = attentionCall(t, s, http.MethodPut, roleFull, `{"mode":"immersive","minutes":30,"focus":{"machine_id":"mac","session_id":"s1"}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}
	a := decodeAttention(t, out["attention"])
	if a.Mode != assistantwire.AttentionImmersive || !a.Until.Equal(now.Add(30*time.Minute)) || a.Focus == nil || a.Focus.SessionID != "s1" {
		t.Fatalf("PUT answered %+v", a)
	}
	// The hub is told in the profile.
	if p := ft.f.lastSnapshot().Profile; p == nil || p.Attention == nil || p.Attention.Mode != assistantwire.AttentionImmersive {
		t.Fatalf("profile %+v", p)
	}
	// An extension reads the mode, never the focus.
	code, out = attentionCall(t, s, http.MethodGet, roleExtension, "")
	if a := decodeAttention(t, out["attention"]); code != http.StatusOK || a.Mode != assistantwire.AttentionImmersive || a.Focus != nil {
		t.Errorf("extension GET: %d %+v", code, a)
	}
	// Immersive mode counts as the low-distraction switch on, and takes questions as ones that can wait.
	if !s.quietModeOn(context.Background()) || !s.attentionDefersQuestions(context.Background()) {
		t.Error("immersive: switch off or questions not deferred")
	}

	// Its time runs out: normal again, by the system, and the profile says nothing of it.
	mu.Lock()
	now = now.Add(31 * time.Minute)
	mu.Unlock()
	if got := s.attentionNow(context.Background()); got.Active() {
		t.Fatalf("after its end: %+v", got)
	}
	if next := s.attentionDue(context.Background()); !next.IsZero() {
		t.Errorf("next end %v", next)
	}
	if stored := s.attentionStored(context.Background()); stored.Mode != assistantwire.AttentionNormal || stored.SetBy != assistantwire.AttentionBySystem {
		t.Errorf("stored after its end: %+v", stored)
	}
	if p := ft.f.lastSnapshot().Profile; p == nil || p.Attention != nil {
		t.Errorf("profile after its end %+v", p)
	}
	if s.quietModeOn(context.Background()) || s.attentionDefersQuestions(context.Background()) {
		t.Error("normal again but the switch still reads on")
	}
}

// The mode is never turned on by a store that does not answer, nor by an end that is already past.
func TestAttentionNormalWithoutAStore(t *testing.T) {
	ft := newFleetTestNoStore(t)
	if a := ft.s.attentionNow(context.Background()); a.Active() {
		t.Fatalf("no store: %+v", a)
	}
	if code, _ := attentionCall(t, ft.s, http.MethodPut, roleFull, `{"mode":"quiet","minutes":30}`); code != http.StatusServiceUnavailable {
		t.Errorf("PUT without a store: %d", code)
	}
}
