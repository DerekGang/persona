package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestProjectEvidenceAcrossKnowledgeInterfaces(t *testing.T) {
	hub := newFakeHub()
	hub.hello.Locale = "en"
	s := newKBService(t, hub)
	const project = "/api/v1/kb/projects/harbor"
	put := func(payload string) {
		t.Helper()
		r := request(s, http.MethodPut, project, payload)
		if r.Code != http.StatusOK {
			t.Fatalf("PUT project: %d %s", r.Code, r.Body.String())
		}
	}
	put(`{"name":"Harbor","summary":"Two items pending"}`)
	put(`{"log":"Both items delivered","source":"synthetic release"}`)
	read := func(path string, body any) {
		t.Helper()
		r := request(s, http.MethodGet, path, "")
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), body) != nil {
			t.Fatalf("GET %s: %d %s", path, r.Code, r.Body.String())
		}
	}
	var detail assistantwire.KBGetResult
	var list assistantwire.KBListResult
	read(project, &detail)
	read("/api/v1/kb/projects", &list)
	if detail.Detail == nil {
		t.Fatal("HTTP detail did not return the project")
	}
	if strings.TrimSpace(detail.Detail.Summary) != "Two items pending" || len(list.Items) != 1 {
		t.Fatalf("summary=%q, project count=%d", detail.Detail.Summary, len(list.Items))
	}
	readRPC := func(method, raw string) any {
		t.Helper()
		out, handled, err := s.kbRequest(t.Context(), method, json.RawMessage(raw))
		if !handled || err != nil {
			t.Fatalf("%s: handled=%v, error=%v", method, handled, err)
		}
		return out
	}
	rpcList := readRPC(assistantwire.MethodKBList, `{"kind":"projects"}`).(assistantwire.KBListResult)
	rpcDetail := readRPC(assistantwire.MethodKBGet, `{"kind":"projects","id":"harbor"}`).(assistantwire.KBGetResult)
	if len(rpcList.Items) != 1 || rpcDetail.Detail == nil {
		t.Fatalf("manager returned %d projects, detail present=%v", len(rpcList.Items), rpcDetail.Detail != nil)
	}
	for _, coverage := range []*kbwire.ProjectKnowledge{detail.Detail.ProjectKnowledge, list.Items[0].ProjectKnowledge, rpcList.Items[0].ProjectKnowledge, rpcDetail.Detail.ProjectKnowledge} {
		if coverage == nil || !coverage.NeedsReview {
			t.Fatalf("project review gap missing from a reader: %+v", coverage)
		}
		field := coverage.Fields["summary"]
		if field.Status != "needs_review" || field.Cutoff == nil || coverage.Position <= *field.Cutoff || field.Uncovered < 1 {
			t.Fatalf("summary coverage: %+v at %d", field, coverage.Position)
		}
	}
	tool := func(name, raw string) string {
		t.Helper()
		result, err := runTool(s, "c1", name, json.RawMessage(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return result
	}
	out := tool("kb_list", `{"kind":"projects"}`)
	if !strings.Contains(out, "Needs review") || !strings.Contains(out, "summary") {
		t.Errorf("tool list omitted summary review: %s", out)
	}
	out = tool("kb_get", `{"kind":"projects","id":"harbor"}`)
	if !strings.Contains(out, `"status":"needs_review"`) || !strings.Contains(out, `"cutoff":`) {
		t.Errorf("tool detail omitted the evidence cutoff: %s", out)
	}

	const link = "https://example.org/projects/harbor"
	put(`{"storage_policy":"reference_only","source_refs":["` + link + `"]}`)
	out = tool("kb_get", `{"kind":"projects","id":"harbor"}`)
	if !strings.Contains(out, `"policy":"reference_only"`) || !strings.Contains(out, link) {
		t.Errorf("tool did not expose the reference-only policy and pointer: %s", out)
	}
	if _, err := runTool(s, "c1", "kb_update", json.RawMessage(`{"kind":"projects","id":"harbor","log":"private fixture detail"}`)); err == nil || !strings.Contains(err.Error(), "reference-only") {
		t.Errorf("tool detail write: %v", err)
	}
	rejected := request(s, http.MethodPut, project, `{"summary":"private fixture detail"}`)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "reference-only") {
		t.Errorf("HTTP detail write: %d %s", rejected.Code, rejected.Body)
	}
	// The same policy applies to extension RPCs; their authenticated identity is supplied by the manager envelope.
	call := assistantwire.KBExtensionUpdate{Extension: "example", Params: json.RawMessage(`{"kind":"projects","id":"harbor","log":"private fixture detail"}`)}
	params, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var refusal *rpc.Error
	if _, handled, err := s.kbRequest(t.Context(), assistantwire.MethodKBExtensionUpdate, params); !handled || !errors.As(err, &refusal) || refusal.Code != bus.CodeInvalidParams || !strings.Contains(refusal.Message, "reference-only") {
		t.Errorf("extension detail write: handled=%v, err=%v", handled, err)
	}
	read(project, &detail)
	if strings.Contains(strings.Join(detail.Detail.Log, "\n"), "private fixture detail") || strings.TrimSpace(detail.Detail.Summary) != "Two items pending" {
		t.Error("a refused write changed the project summary or timeline")
	}
	put(`{"storage_policy":"managed"}`)
	put(`{"log":"New event after choosing managed storage"}`)
	read(project, &detail)
	if !strings.Contains(strings.Join(detail.Detail.Log, "\n"), "New event after choosing managed storage") {
		t.Fatal("explicitly returning to managed storage did not enable future writes")
	}
}
