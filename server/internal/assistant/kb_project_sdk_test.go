package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestExtensionSDKReadsProjectEvidence(t *testing.T) {
	s := newKBService(t, newFakeHub())
	const path = "/api/v1/kb/projects/example"
	for _, body := range []string{
		`{"summary":"Release planned","source_refs":["docs/plan"]}`,
		`{"log":"Release observed"}`,
	} {
		if r := request(s, http.MethodPut, path, body); r.Code != http.StatusOK {
			t.Fatalf("create fixture: %d %s", r.Code, r.Body.String())
		}
	}

	// Read the manager's real responses through the extension SDK's process protocol.
	manifest := []byte(`{"manifest_version":1,"id":"example","name":"Example","version":"1.0.0",
		"engines":{"extension_protocol":1},"runtime":{"command":"example"},"permissions":{"hub":["kb.read"]}}`)
	var sdk *ext.Hub
	p := ext.Program{ID: "example", Version: "1.0.0", Manifest: manifest,
		Init: func(_ context.Context, _ *ext.Init, h *ext.Hub) (map[string]any, error) {
			sdk = h
			return nil, nil
		}}
	host := exttest.Start(t, manifest, exttest.Serve(t, p))
	ctx := t.Context()
	for _, method := range []string{assistantwire.MethodKBList, assistantwire.MethodKBGet} {
		host.Answer(method, func(params json.RawMessage) (any, error) {
			result, _, err := s.kbRequest(ctx, method, params)
			return result, err
		})
	}
	list, err := sdk.KBList(ctx, wire.KBListParams{Kind: wire.KBKindProjects})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("kb.list: %+v %v", list, err)
	}
	get, err := sdk.KBGet(ctx, wire.KBGetParams{Kind: wire.KBKindProjects, ID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []*wire.KBProjectKnowledge{list.Items[0].ProjectKnowledge, get.Detail.ProjectKnowledge} {
		if k == nil || k.Policy != "managed" || !k.NeedsReview {
			t.Fatalf("missing project review: %+v", k)
		}
		f := k.Fields["summary"]
		if f.Cutoff == nil || f.Uncovered != k.Position-*f.Cutoff || f.Uncovered < 1 || !f.Locked {
			t.Fatalf("summary coverage: %+v at %d", f, k.Position)
		}
	}
	if len(list.Items[0].ProjectKnowledge.Sources) != 0 || len(get.Detail.ProjectKnowledge.Sources) == 0 {
		t.Fatal("resolved sources belong to the detail")
	}

	// A program can send raw JSON. The authenticated write path must still reject owner-only fields.
	for _, field := range []string{`"storage_policy":"reference_only"`, `"source_refs":[]`} {
		params := json.RawMessage(`{"kind":"projects","id":"example",` + field + `}`)
		if err := wire.ValidateParams("kb.update", params); err == nil {
			t.Fatalf("SDK schema accepted %s", field)
		}
		raw, err := json.Marshal(assistantwire.KBExtensionUpdate{Extension: "example", Params: params})
		if err != nil {
			t.Fatal(err)
		}
		if _, handled, err := s.kbRequest(ctx, assistantwire.MethodKBExtensionUpdate, raw); !handled || err == nil || !strings.Contains(err.Error(), "only the owner") {
			t.Fatalf("manager accepted %s: handled=%v err=%v", field, handled, err)
		}
	}
	get, err = sdk.KBGet(ctx, wire.KBGetParams{Kind: wire.KBKindProjects, ID: "example"})
	if err != nil || get.Detail.ProjectKnowledge == nil || get.Detail.ProjectKnowledge.Policy != "managed" ||
		!slices.ContainsFunc(get.Detail.ProjectKnowledge.Sources, func(s wire.KBProjectSource) bool { return s.Ref == "docs/plan" }) {
		t.Fatalf("owner's choices after the refused writes: %+v, %v", get.Detail.ProjectKnowledge, err)
	}

	host.Answer(assistantwire.MethodKBGet, func(json.RawMessage) (any, error) {
		return json.RawMessage(`{"detail":{"kind":"projects","id":"example","path":"projects/example","meta":{}}}`), nil
	})
	legacy, err := sdk.KBGet(ctx, wire.KBGetParams{Kind: wire.KBKindProjects, ID: "example"})
	if err != nil || legacy.Detail.ID != "example" || legacy.Detail.ProjectKnowledge != nil {
		t.Fatalf("older hub detail: %+v, %v", legacy, err)
	}
}
