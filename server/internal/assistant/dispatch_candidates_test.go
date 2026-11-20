package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// GET /api/v1/assistant/dispatch/candidates shares the worker-presets inventory but answers with its own credential
// allowlist and a candidates envelope.
func TestDispatchCandidatesMatchesWorkerPresetsAndStaysReadOnly(t *testing.T) {
	s, f := defaultsHub(t)
	ctx := context.Background()
	canary := "FAKE_CANARY_TOKEN_dispatch_candidates"
	external := externalTestLauncher()
	external.External.CredentialState = canary
	f.zshrc["box"] = append(f.zshrc["box"],
		proto.Launcher{Name: "hidden-ok", Source: "zshrc"},
		proto.Launcher{Name: "hidden-bad", Source: "zshrc"},
		external,
	)
	f.zshrc["box"][0].BaseURL = "https://" + canary + ".example"
	for i := range f.devices {
		if f.devices[i].ID == "box" {
			f.devices[i].Name = "Build Box"
			f.devices[i].SSHHost = "box-alias"
			f.devices[i].ExternalInventory = true
		}
	}
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"hidden-ok", "hidden-bad"}); err != nil {
		t.Fatal(err)
	}
	saveTiers(t, s, map[string]ModelTier{
		"deep":       {Launcher: "gateway", Model: "gw-deep", Effort: "high", Speed: "standard", Description: "deep tier"},
		"quick":      {Launcher: "gateway", Model: "gw-quick", Speed: "fast"},
		"plain":      {Launcher: "claude", Model: "claude-plain"},
		"missing":    {Launcher: "other", Model: "o-1"},
		"external":   {Launcher: external.Name, Model: "vendor/model-one"},
		"hidden-bad": {Launcher: "hidden-bad", Model: "bad-model"},
		"hidden-ok":  {Launcher: "hidden-ok", Model: "hidden-model"},
	})
	resets := time.Now().Add(2 * time.Hour)
	for _, launcher := range []string{"gateway", "hidden-bad"} {
		s.recordHealth(ctx, "box", launcher, healthResult{err: canary, source: healthSourceSession,
			kind: errKindAuth, resetsAt: &resets})
	}

	beforePuts := s.store().(*profileTaskStore).memStore.puts
	beforeListCalls := len(f.launcherListParams)
	beforeStarts := len(startsOf(f))
	allowed := map[string]bool{
		"name": true, "launcher": true, "model": true, "effort": true,
		"speed": true, "description": true, "unavailable": true,
	}
	want := []dispatchCandidate{
		{Name: "deep", Launcher: "gateway", Model: "gw-deep", Effort: "high", Speed: "standard",
			Description: "deep tier", Unavailable: true},
		{Name: "hidden-ok", Launcher: "hidden-ok", Model: "hidden-model"},
		{Name: "plain", Launcher: "claude", Model: "claude-plain"},
		{Name: "quick", Launcher: "gateway", Model: "gw-quick", Speed: "fast", Unavailable: true},
	}
	for _, machine := range []string{"box", "Build Box", "box-alias"} {
		query := "machine=" + url.QueryEscape(machine)
		workerRec := httptest.NewRecorder()
		s.ServeHTTP(workerRec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/worker-presets?"+query, nil))
		if workerRec.Code != http.StatusOK {
			t.Fatalf("worker-presets for %q: %d %s", machine, workerRec.Code, workerRec.Body.String())
		}
		candidateRec := httptest.NewRecorder()
		s.ServeHTTP(candidateRec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/dispatch/candidates?"+query, nil))
		if candidateRec.Code != http.StatusOK {
			t.Fatalf("candidates for %q: %d %s", machine, candidateRec.Code, candidateRec.Body.String())
		}
		var presetsBody struct {
			Presets []workerPreset `json:"presets"`
		}
		if err := json.Unmarshal(workerRec.Body.Bytes(), &presetsBody); err != nil {
			t.Fatal(err)
		}
		var candidateBody struct {
			Candidates []dispatchCandidate `json:"candidates"`
		}
		if err := json.Unmarshal(candidateRec.Body.Bytes(), &candidateBody); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(candidateBody.Candidates, want) {
			t.Fatalf("candidates for %q = %#v, want %#v", machine, candidateBody.Candidates, want)
		}
		if got := toDispatchCandidates(presetsBody.Presets); !reflect.DeepEqual(got, want) {
			t.Fatalf("the routes diverged for %q: %#v, want %#v", machine, got, want)
		}
		if strings.Contains(candidateRec.Body.String(), canary) {
			t.Fatalf("the response exposed a credential canary: %s", candidateRec.Body.String())
		}
		var envelope map[string][]map[string]json.RawMessage
		if err := json.Unmarshal(candidateRec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		for _, row := range envelope["candidates"] {
			for key := range row {
				if !allowed[key] {
					t.Fatalf("unallowed response field %q in %s", key, candidateRec.Body.String())
				}
			}
		}
	}

	if got := len(startsOf(f)); got != beforeStarts {
		t.Fatalf("the read started an Agent: got %d starts, want %d", got, beforeStarts)
	}
	if got := s.store().(*profileTaskStore).memStore.puts; got != beforePuts {
		t.Fatalf("the read wrote settings: got %d puts, want %d", got, beforePuts)
	}
	for _, params := range f.launcherListParams[beforeListCalls:] {
		if refresh, _ := params["refresh"].(bool); refresh {
			t.Fatalf("the read forced a launcher refresh: %v", params)
		}
	}

	// No active profile: both inventories stay empty arrays and do not fail.
	if err := s.store().PutSetting(ctx, modelProfileStateKey, modelProfileState{}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/dispatch/candidates?machine=box", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"candidates":[]}` {
		t.Fatalf("empty candidates = %d %q", rec.Code, rec.Body.String())
	}
}

// dispatchTestLocaleWriter carries the same language interface as the manager's normal HTTP writer.
type dispatchTestLocaleWriter struct {
	http.ResponseWriter
	lang i18n.Lang
}

func (w *dispatchTestLocaleWriter) Lang() i18n.Lang             { return w.lang }
func (w *dispatchTestLocaleWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// The new route preserves the 502 status class but never repeats internal launcher or credential errors.
func TestDispatchCandidatesInventoryErrorIsPublicAndFixed(t *testing.T) {
	s, f := defaultsHub(t)
	canary := "FAKE_CANARY_TOKEN_inventory_error"
	f.answerNode(DeviceLaunchersList, func(_ string, _ json.RawMessage) (any, error) {
		return nil, errors.New(canary)
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/dispatch/candidates?machine=box", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "launcher inventory unavailable" || body.Code != "internal" {
		t.Fatalf("body = %#v", body)
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("the internal error leaked: %s", rec.Body.String())
	}

	// The production manager wraps responses with a locale-bearing writer. Its generic internal-error localization
	// must not replace the fixed public field or move it to detail in any shipped language.
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		t.Run(string(lang), func(t *testing.T) {
			localized := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/dispatch/candidates?machine=box", nil)
			req.Header.Set("Accept-Language", string(lang))
			s.ServeHTTP(&dispatchTestLocaleWriter{ResponseWriter: localized, lang: lang}, req)
			if localized.Code != http.StatusBadGateway {
				t.Fatalf("status = %d", localized.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(localized.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["error"] != "launcher inventory unavailable" || body["code"] != "internal" || body["detail"] != nil {
				t.Fatalf("localized body = %v", body)
			}
			if strings.Contains(localized.Body.String(), canary) {
				t.Fatalf("the internal error leaked through the localized writer: %s", localized.Body.String())
			}
		})
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/dispatch/candidates", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown machine") {
		t.Fatalf("omitted machine = %d %s", rec.Code, rec.Body.String())
	}
}
