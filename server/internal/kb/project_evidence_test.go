package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

func projectRead(t *testing.T, e *Engine, id string) (*Detail, *kbwire.ProjectKnowledge) {
	t.Helper()
	d, err := e.Get(KindProject, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range e.List(KindProject, "") {
		if r.ID == id {
			if d.ProjectKnowledge == nil || r.ProjectKnowledge == nil {
				t.Fatal("project reads omit knowledge")
			}
			compact := *d.ProjectKnowledge
			compact.Sources = nil
			if !reflect.DeepEqual(&compact, r.ProjectKnowledge) {
				t.Fatalf("list/detail disagree: %+v / %+v", r.ProjectKnowledge, d.ProjectKnowledge)
			}
			return d, d.ProjectKnowledge
		}
	}
	t.Fatalf("no project %q", id)
	return nil, nil
}

func TestProjectEvidenceLockedSummarySurvivesNewSourceAndReload(t *testing.T) {
	ctx := context.Background()
	m := &captureModel{reply: `{"summary":"All delivered","one_line":"All delivered"}`}
	e := testEngine(t, m)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	c := prChat(t, e, "fixture-a", "delivery", at)
	editApply(t, e, KindProject, "delivery", Update{Summary: "Two items pending"})
	_, old := projectRead(t, e, "delivery")
	original := old.Fields["summary"]
	if original.Writer != "user" || !original.Locked || original.Cutoff == nil {
		t.Fatalf("manual receipt %+v", original)
	}
	event := SourceChat{ID: c.SourceID, Kind: "pr", State: "merged", StateAt: at.Add(time.Minute)}
	for range 2 {
		if _, err := e.Ingest(ctx, named("github"), event, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.RefreshProjects(ctx, 1); got != 1 {
		t.Fatalf("refresh %d", got)
	}
	store, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d, k := projectRead(t, &Engine{Store: store}, "delivery")
	summary, line := k.Fields["summary"], k.Fields["one_line"]
	if strings.TrimSpace(d.Summary) != "Two items pending" || !k.NeedsReview || k.Position != old.Position+1 ||
		summary.Status != "needs_review" || !summary.Locked || summary.Cutoff == nil || *summary.Cutoff != *original.Cutoff ||
		summary.Uncovered != 1 || line.Status != "current" || line.Writer != "pipeline" || *line.Cutoff != k.Position {
		t.Fatalf("unexpected coverage %+v %+v / %s", k, summary, d.Summary)
	}
	ref := KindChat + "/" + c.ID
	if !slices.Contains(line.SourceRefs, ref) {
		t.Fatalf("missing canonical source: %v", line.SourceRefs)
	}
	var count int
	for _, s := range k.Sources {
		if s.Ref == ref {
			count++
			if s.State != "merged" || s.StateAt == nil || !s.StateAt.Equal(event.StateAt) {
				t.Fatalf("source %+v", s)
			}
		}
	}
	if count != 1 || strings.Contains(string(d.Meta), "field_evidence") {
		t.Fatalf("source count %d, internal data exposed in %s", count, d.Meta)
	}
}

func TestProjectEvidenceLegacyFailurePartialAndManual(t *testing.T) {
	e := testEngine(t, &captureModel{reply: `{"summary":"First","one_line":"First"}`})
	dirtyProject(t, e, "fixture")
	if _, k := projectRead(t, e, "fixture"); k.NeedsReview || k.Fields["summary"].Status != "unknown" || k.Fields["summary"].Cutoff != nil || k.Fields["summary"].Writer != "" {
		t.Fatalf("invented legacy receipt %+v", k)
	}
	p, _ := e.Store.Project("fixture")
	if err := e.refreshProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, old := projectRead(t, e, "fixture")
	if *old.Fields["summary"].Cutoff != 0 || *old.Fields["one_line"].Cutoff != 0 {
		t.Fatal("initial cutoff should be zero")
	}
	editApply(t, e, KindProject, "fixture", Update{Log: "Second fact", Source: "Fixture"})
	e.Model = editModel{run: func(string, any) error { return errors.New("fixture unavailable") }}
	p, _ = e.Store.Project("fixture")
	if err := e.refreshProject(context.Background(), p); err == nil {
		t.Fatal("expected model failure")
	}
	d, k := projectRead(t, e, "fixture")
	if k.Position != 1 || !k.NeedsReview || *k.Fields["summary"].Cutoff != 0 || *k.Fields["one_line"].Cutoff != 0 || len(d.Log) != 1 {
		t.Fatalf("failure advanced either field %+v", k)
	}
	e.Model = &captureModel{reply: `{"one_line":"Second fact"}`}
	if err := e.refreshProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, k = projectRead(t, e, "fixture")
	if k.Fields["summary"].Status != "needs_review" || k.Fields["one_line"].Status != "current" {
		t.Fatalf("partial write %+v", k)
	}
	e.Model = &captureModel{reply: `{"summary":"Second fact","one_line":"Second fact"}`}
	p, _ = e.Store.Project("fixture")
	if err := e.refreshProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, k = projectRead(t, e, "fixture"); k.NeedsReview {
		t.Fatalf("successful rewrite %+v", k)
	}
	editApply(t, e, KindProject, "fixture", Update{Summary: "A correction"})
	d, k = projectRead(t, e, "fixture")
	if strings.TrimSpace(d.Summary) != "A correction" || !k.NeedsReview || k.Fields["summary"].Writer != "user" || k.Fields["summary"].Status != "current" || k.Fields["one_line"].Status != "needs_review" {
		t.Fatalf("manual correction %+v", k)
	}
	// A metadata-only change cannot advance the proof of either field.
	editApply(t, e, KindProject, "fixture", Update{Aliases: []string{"alternate"}})
	if _, again := projectRead(t, e, "fixture"); !reflect.DeepEqual(k, again) {
		t.Fatalf("alias advanced evidence %+v / %+v", k, again)
	}
}

func TestProjectReferenceOnlyGuardsWritesKeepsHistoryAndAllowsPointers(t *testing.T) {
	e := testEngine(t, &captureModel{})
	slug := "reference"
	editApply(t, e, KindProject, slug, Update{Summary: "Old summary", Log: "Old log"})
	old, _ := e.Store.Project(slug)
	c := prChat(t, e, "shared", "", time.Now().UTC())
	ref := KindChat + "/" + c.ID
	editApply(t, e, KindProject, slug, Update{StoragePolicy: referenceOnly, SourceRefs: []string{"HTTPS://EXAMPLE.org/release", ref}, Aliases: []string{"handbook"}, Chats: []string{c.ID}})
	reopened, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Store = reopened
	for _, u := range []Update{{Summary: "private fixture detail", Aliases: []string{"bad"}}, {Log: "private fixture detail"}, {Note: "private fixture detail"}, {Goal: "private fixture detail"}, {MyRole: "private fixture detail"}} {
		if _, err := e.Apply(KindProject, slug, u); !errors.Is(err, ErrReferenceOnly) {
			t.Fatalf("accepted forbidden fields %+v: %v", u, err)
		}
	}
	if _, err := e.ApplyExtension(KindProject, slug, Update{StoragePolicy: "managed"}, ExtensionWrite{Extension: "fixture"}); err == nil {
		t.Fatal("extension changed policy")
	}
	if _, err := e.ApplyExtension(KindProject, slug, Update{Log: "private fixture detail"}, ExtensionWrite{Extension: "fixture"}); !errors.Is(err, ErrReferenceOnly) {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(KindProject, slug, "private fixture detail"); !errors.Is(err, ErrReferenceOnly) {
		t.Fatal(err)
	}
	if err := e.Store.AppendLog(slug, time.Now(), "Fixture", "private fixture detail"); !errors.Is(err, ErrReferenceOnly) {
		t.Fatal(err)
	}
	if err := e.AppendProjectMemory(slug, ProjectMemoryRecord{ID: "item", Conclusion: "private fixture detail"}); !errors.Is(err, ErrReferenceOnly) {
		t.Fatal(err)
	}
	old.OneLine, old.Goal, old.Dirty = "private fixture detail", "private fixture detail", true
	if err := e.Store.PutProject(old); err != nil {
		t.Fatal(err)
	}
	e.evolveProject(slug, "short", nil, "private fixture detail", c, "private fixture detail", time.Now())
	if got := e.RefreshProjects(context.Background(), 2); got != 0 || len(e.Model.(*captureModel).calls) != 0 {
		t.Fatalf("reference project asked the model: %d", got)
	}
	d, k := projectRead(t, e, slug)
	if k.Policy != referenceOnly || len(d.Log) != 1 || !strings.Contains(d.Log[0], "Old log") || strings.TrimSpace(d.Summary) != "Old summary" {
		t.Fatalf("history changed %+v %+v", d, k)
	}
	seen := map[string]int{}
	for _, s := range k.Sources {
		seen[s.Ref]++
	}
	if seen[ref] != 1 || seen["https://example.org/release"] != 1 {
		t.Fatalf("sources %v", seen)
	}
	p, _ := e.Store.Project(slug)
	if !slices.Contains(p.Aliases, "handbook") || slices.Contains(p.Aliases, "bad") || p.MyRole != "" {
		t.Fatalf("scope leaked %+v", p)
	}
	for _, f := range []string{"meta.json", "summary.md", "log.md", "memory.jsonl"} {
		v, err := os.ReadFile(e.Store.path(KindProject, slug, f))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(v), "private fixture detail") {
			t.Fatalf("persisted payload in %s", f)
		}
	}
	editApply(t, e, KindProject, slug, Update{StoragePolicy: "managed", SourceRefs: []string{}, Log: "Managed again"})
	d, k = projectRead(t, e, slug)
	if k.Policy != "managed" || len(d.Log) != 2 || !strings.Contains(d.Log[1], "Managed again") {
		t.Fatalf("failed to restore managed storage %+v", d)
	}
}

func TestProjectEvidenceRejectsLateRefreshAcrossPolicyChange(t *testing.T) {
	e := testEngine(t, nil)
	dirtyProject(t, e, "fixture")
	p, _ := e.Store.Project("fixture")
	started, release := make(chan struct{}), make(chan struct{})
	e.Model = editModel{run: func(_ string, out any) error {
		close(started)
		<-release
		return json.Unmarshal([]byte(`{"summary":"obsolete","one_line":"obsolete"}`), out)
	}}
	done := make(chan error, 1)
	go func() { done <- e.refreshProject(context.Background(), p) }()
	<-started
	editApply(t, e, KindProject, "fixture", Update{StoragePolicy: referenceOnly, SourceRefs: []string{"https://example.org/state"}})
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d, k := projectRead(t, e, "fixture")
	if strings.Contains(d.Summary, "obsolete") || strings.Contains(string(d.Meta), "obsolete") || k.Fields["summary"].Cutoff != nil || k.Policy != referenceOnly {
		t.Fatalf("late result %+v", d)
	}
}

func TestProjectKnowledgeExplicitLinksStrictAndReversible(t *testing.T) {
	e := testEngine(t, nil)
	editApply(t, e, KindProject, "p", Update{SourceRefs: []string{"HTTP://EXAMPLE.ORG/r?b=2#p", "http://example.org/r?b=2#p", "docs/note"}})
	p, _ := e.Store.Project("p")
	if !slices.Equal(p.SourceRefs, []string{"http://example.org/r?b=2#p", "docs/note"}) {
		t.Fatal(p.SourceRefs)
	}
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("docs/%d", i)
	}
	for _, bad := range []Update{{StoragePolicy: "typo"}, {SourceRefs: []string{"file:///fixture"}}, {SourceRefs: []string{"https://user@example.org/"}}, {SourceRefs: []string{"docs/../test"}}, {SourceRefs: tooMany}} {
		if _, err := e.Apply(KindProject, "p", bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	p, _ = e.Store.Project("p")
	if len(p.SourceRefs) != 2 {
		t.Fatal("invalid edit replaced valid refs")
	}
	raw, err := json.Marshal(Update{SourceRefs: []string{}})
	if err != nil || !strings.Contains(string(raw), `"source_refs":[]`) {
		t.Fatalf("explicit clear lost on RPC wire: %s %v", raw, err)
	}
	editApply(t, e, KindProject, "p", Update{SourceRefs: []string{}})
	p, _ = e.Store.Project("p")
	if len(p.SourceRefs) != 0 {
		t.Fatal(p.SourceRefs)
	}
}

func TestProjectEvidenceSharedCanonicalSourceAndErasure(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t, &captureModel{reply: `{"summary":"The source merged","one_line":"Merged"}`})
	at := time.Now().Add(-time.Hour).UTC()
	c := prChat(t, e, "common", "first", at)
	editApply(t, e, KindProject, "second", Update{Chats: []string{c.ID}, SourceRefs: []string{KindChat + "/" + c.ID}})
	event := SourceChat{ID: c.SourceID, Kind: "pr", State: "merged", StateAt: at.Add(time.Minute)}
	if _, err := e.Ingest(ctx, named("github"), event, nil); err != nil {
		t.Fatal(err)
	}
	if e.RefreshProjects(ctx, 2) != 2 {
		t.Fatal("each linked project should refresh")
	}
	for _, slug := range []string{"first", "second"} {
		_, k := projectRead(t, e, slug)
		var shared []kbwire.ProjectSource
		for _, s := range k.Sources {
			if s.Ref == KindChat+"/"+c.ID {
				shared = append(shared, s)
			}
		}
		if len(shared) != 1 || shared[0].State != "merged" || k.Position != 1 {
			t.Fatalf("%s: %+v", slug, k)
		}
	}
	// Explicit forgetting still erases original pointers, even if also kept in an index or a field receipt.
	if _, err := e.ForgetSource("github", "github"); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"first", "second"} {
		d, _ := projectRead(t, e, slug)
		raw, err := os.ReadFile(e.Store.entityPath(KindProject, slug))
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(d)
		if strings.Contains(string(wire), c.ID) || strings.Contains(string(raw), c.ID) {
			t.Fatalf("forgotten source survived: %s", raw)
		}
	}
}

func TestProjectEvidenceUncompressedContentAndPartialSourceWindow(t *testing.T) {
	ctx := context.Background()
	m := &captureModel{reply: `{"summary":"Initial result","one_line":"Initial result"}`}
	e := testEngine(t, m)
	at := time.Now().Add(-time.Hour).UTC()
	c := prChat(t, e, "turns", "fixture", at)
	p, _ := e.Store.Project("fixture")
	if err := e.refreshProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	msg := SourceMessage{ID: "next", SenderID: "alice", SenderName: "Alice", Text: "The fixture changed", At: at.Add(time.Minute)}
	for i := range 2 {
		n, err := e.Ingest(ctx, named("github"), SourceChat{ID: c.SourceID, Kind: "pr"}, []SourceMessage{msg})
		if err != nil || n != 1-i {
			t.Fatalf("ingest pass %d: %d %v", i, n, err)
		}
	}
	p, _ = e.Store.Project("fixture")
	if err := e.refreshProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, k := projectRead(t, e, "fixture")
	if k.Position != 1 || !k.NeedsReview || *k.Fields["summary"].Cutoff != 0 {
		t.Fatalf("an unread source was marked covered: %+v", k)
	}
	c, _ = e.Store.Chat(c.ID)
	e.Model = &captureModel{reply: `{"summary":"Changed","one_line":"Changed","projects":[{"name":"fixture","update":"New result"}]}`}
	if err := e.Compress(ctx, c); err != nil {
		t.Fatal(err)
	}
	e.Model = m
	p, _ = e.Store.Project("fixture")
	if err := e.refreshProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, k = projectRead(t, e, "fixture")
	if k.NeedsReview || k.Position != 1 || !slices.Contains(k.Fields["summary"].SourceRefs, KindChat+"/"+c.ID) {
		t.Fatalf("same underlying input was duplicated or omitted: %+v", k)
	}
	// A connector state outside the rewrite's eight actual chat inputs remains outstanding.
	for i := 0; i < 8; i++ {
		prChat(t, e, fmt.Sprintf("extra%d", i), "fixture", at)
	}
	last := ChatID("github", "extra7")
	if _, err := e.Ingest(ctx, named("github"), SourceChat{ID: "extra7", Kind: "pr", State: "merged", StateAt: at.Add(2 * time.Minute)}, nil); err != nil {
		t.Fatal(err)
	}
	p, _ = e.Store.Project("fixture")
	if err := e.refreshProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, k = projectRead(t, e, "fixture")
	if !k.NeedsReview || slices.Contains(k.Fields["summary"].SourceRefs, KindChat+"/"+last) {
		t.Fatalf("capped model input falsely covered a ninth chat: %+v", k)
	}
}

func TestProjectEvidenceExternalWriterAndLegacyLockedOwner(t *testing.T) {
	e := testEngine(t, nil)
	_ = e.Store.PutProject(&Project{Slug: "legacy", Name: "Legacy", Tagging: Tagging{LockedFields: []string{"summary"}}})
	_, k := projectRead(t, e, "legacy")
	f := k.Fields["summary"]
	if f.Writer != "" || f.Cutoff != nil || f.Maintainer != "user" || !f.Locked {
		t.Fatalf("historical lock falsely attributed: %+v", f)
	}
	c := prChat(t, e, "writer-source", "extension", time.Now().UTC())
	if _, err := e.ApplyExtension(KindProject, "extension", Update{Summary: "Test note"}, ExtensionWrite{Extension: "curator", Evidence: []Evidence{{Chat: c.ID, Day: "2026-11-11", N: 1}}}); err != nil {
		t.Fatal(err)
	}
	_, k = projectRead(t, e, "extension")
	f = k.Fields["summary"]
	if f.Maintainer != "ext:curator" || f.Writer != "ext:curator" || !slices.Contains(f.SourceRefs, KindChat+"/"+c.ID) {
		t.Fatalf("extension receipt %+v", f)
	}
}

func TestProjectEvidenceConcurrentLogWritesAndReads(t *testing.T) {
	e := testEngine(t, nil)
	editApply(t, e, KindProject, "shared", Update{Summary: "Original"})
	_, initial := projectRead(t, e, "shared")
	const workers = 12
	errs := make(chan error, workers)
	for i := range workers {
		go func() {
			err := e.Store.AppendLog("shared", time.Now(), "Fixture", fmt.Sprintf("Update %d", i))
			if err == nil {
				d, readErr := e.Get(KindProject, "shared", 0)
				err = readErr
				if err == nil && d.ProjectKnowledge.Position != initial.Position+len(d.Log) {
					err = fmt.Errorf("timeline and receipt diverged: %d vs. %d", len(d.Log), d.ProjectKnowledge.Position)
				}
			}
			errs <- err
		}()
	}
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	st, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d, k := projectRead(t, &Engine{Store: st}, "shared")
	if k.Position != initial.Position+workers || k.Fields["summary"].Uncovered != workers || len(d.Log) != workers {
		t.Fatalf("lost writes %+v; %d lines", k, len(d.Log))
	}
}

func TestProjectEvidenceStorageFailurePreservesPreviousReceipt(t *testing.T) {
	e := testEngine(t, &captureModel{reply: `{"summary":"Initial","one_line":"Initial"}`})
	dirtyProject(t, e, "project")
	p, _ := e.Store.Project("project")
	if err := e.refreshProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	editApply(t, e, KindProject, "project", Update{Log: "An observed fact"})
	before, k := projectRead(t, e, "project")
	badSummary := e.Store.path(KindProject, "project", "summary.md")
	if err := os.Rename(badSummary, badSummary+".fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(badSummary, 0700); err != nil {
		t.Fatal(err)
	}
	e.Model = &captureModel{reply: `{"summary":"Replacement","one_line":"Replacement"}`}
	p, _ = e.Store.Project("project")
	if err := e.refreshProject(context.Background(), p); err == nil {
		t.Fatal("expected write failure")
	}
	if err := os.Remove(badSummary); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(badSummary+".fixture", badSummary); err != nil {
		t.Fatal(err)
	}
	after, again := projectRead(t, e, "project")
	if !reflect.DeepEqual(k, again) || before.Summary != after.Summary || !reflect.DeepEqual(before.Log, after.Log) {
		t.Fatalf("failed storage advanced coverage %+v / %+v", k, again)
	}
}

func TestProjectKnowledgeSharedPointersSurviveManualMergeAndUndo(t *testing.T) {
	e := testEngine(t, nil)
	const common = "https://example.org/repository/change"
	editApply(t, e, KindProject, "kept", Update{SourceRefs: []string{common, "docs/a"}})
	for _, id := range []string{"left", "right"} {
		editApply(t, e, KindProject, id, Update{SourceRefs: []string{common, "docs/b", "docs/" + id}})
	}
	if err := e.MergeProjects("kept", "left", "fixture automatic match"); err == nil {
		t.Fatal("automatic match merged a deliberately separate source index")
	}
	expectRefs := func(id string, want ...string) {
		t.Helper()
		p, ok := e.Store.Project(id)
		if !ok || !slices.Equal(p.SourceRefs, want) {
			t.Fatalf("%s: got %+v; wanted %v", id, p, want)
		}
	}
	for _, id := range []string{"left", "right"} {
		editApply(t, e, KindProject, id, Update{MergeInto: "kept"})
	}
	expectRefs("kept", common, "docs/a", "docs/b", "docs/left", "docs/right")
	for _, id := range []string{"left", "right"} {
		if _, err := e.Unmerge(id); err != nil {
			t.Fatal(err)
		}
		expectRefs(id, common, "docs/b", "docs/"+id)
		if id == "left" {
			expectRefs("kept", common, "docs/a", "docs/b", "docs/right")
		}
	}
	expectRefs("kept", common, "docs/a")
}

func TestProjectEvidenceLogRollsBackWhenReceiptCannotBeSaved(t *testing.T) {
	e := testEngine(t, nil)
	editApply(t, e, KindProject, "project", Update{Summary: "Recorded", Log: "Known event"})
	before, k := projectRead(t, e, "project")
	dir := e.Store.path(KindProject, "project")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode().Perm()) })
	if err := os.Chmod(dir, 0555); err != nil {
		t.Skipf("filesystem cannot make the metadata directory read-only: %v", err)
	}
	if probe, err := os.CreateTemp(dir, ".fixture-*"); err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("filesystem permits writes to a read-only directory")
	}
	// The existing timeline is still writable, but replacing the adjacent metadata requires directory permission.
	if err := e.Store.AppendLog("project", time.Now(), "Fixture", "Uncommitted"); err == nil {
		t.Fatal("expected metadata write failure")
	}
	after, again := projectRead(t, e, "project")
	if !reflect.DeepEqual(k, again) || !slices.Equal(before.Log, after.Log) {
		t.Fatalf("failed event left text or receipt: %v/%v %+v/%+v", before.Log, after.Log, k, again)
	}
}

func TestProjectKnowledgeTwoViewsOfAnObservedChatStayIndependent(t *testing.T) {
	e := testEngine(t, nil)
	at := time.Now().UTC().Add(-time.Hour)
	c := prChat(t, e, "shared-pr", "main-view", at)
	if err := e.Store.PutProject(&Project{Slug: "team-view", Name: "Team", Chats: []string{c.ID}}); err != nil {
		t.Fatal(err)
	}
	c.Projects = append(c.Projects, "team-view")
	if err := e.Store.PutChat(c); err != nil {
		t.Fatal(err)
	}
	event := SourceChat{ID: c.SourceID, Kind: "pr", State: "merged", StateAt: at.Add(time.Minute)}
	if _, err := e.Ingest(context.Background(), named("github"), event, nil); err != nil {
		t.Fatal(err)
	}
	candidates := dedupeCandidates(e.Store.Projects())
	if len(candidates) != 0 || e.MergeProjects("main-view", "team-view", "same chat") == nil {
		t.Fatal("a shared canonical source made two project views eligible for automatic merging")
	}
	for _, id := range []string{"main-view", "team-view"} {
		_, k := projectRead(t, e, id)
		if len(k.Sources) != 1 || k.Sources[0].Ref != KindChat+"/"+c.ID || k.Sources[0].State != "merged" {
			t.Fatalf("%s did not resolve the same live source: %+v", id, k.Sources)
		}
	}
}

func TestProjectEvidenceExplicitChatPointerReceivesAndCoversNewSourceState(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t, &captureModel{reply: `{"summary":"Source is merged","one_line":"Merged"}`})
	at := time.Now().UTC().Add(-time.Hour)
	c := prChat(t, e, "observed-only", "", at)
	ref := KindChat + "/" + c.ID
	editApply(t, e, KindProject, "index", Update{SourceRefs: []string{ref}})
	// Exercise cold and warm reverse lookup, including removal of an explicit pointer.
	st, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.Store = st
	for _, state := range []string{"closed", "merged"} {
		at = at.Add(time.Minute)
		if _, err := e.Ingest(ctx, named("github"), SourceChat{ID: c.SourceID, Kind: "pr", State: state, StateAt: at}, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, k := projectRead(t, e, "index")
	if k.Position != 2 || !k.NeedsReview || len(k.Sources) != 1 || k.Sources[0].State != "merged" {
		t.Fatalf("explicit local pointer missed its canonical state: %+v", k)
	}
	if e.RefreshProjects(ctx, 1) != 1 {
		t.Fatal("managed index was not refreshed")
	}
	_, k = projectRead(t, e, "index")
	if k.Fields["summary"].Status != "current" || !slices.Contains(k.Fields["summary"].SourceRefs, ref) {
		t.Fatalf("model did not see the canonical evidence: %+v", k)
	}
	editApply(t, e, KindProject, "index", Update{SourceRefs: []string{}})
	if _, err := e.Ingest(ctx, named("github"), SourceChat{ID: c.SourceID, Kind: "pr", State: "open", StateAt: at.Add(time.Minute)}, nil); err != nil {
		t.Fatal(err)
	}
	_, after := projectRead(t, e, "index")
	if after.Position != k.Position {
		t.Fatalf("removed reference still received new evidence: %+v", after)
	}
}

func TestProjectEvidenceLinkedRecordingResolvesOutsideMetadataLock(t *testing.T) {
	e := testEngine(t, nil)
	now := time.Now()
	if _, err := e.PutRecording(recID, recUpload(1, now, false, "Fixture"), now); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutProject(&Project{Slug: "project", Name: "Project", Items: []string{KindRecording + "/" + recID}}); err != nil {
		t.Fatal(err)
	}
	observed := false
	e.Store.recordingReadWait = func() {
		observed = true
		if !e.Store.metaMu.TryLock() {
			t.Fatal("Get holds metaMu before acquiring the recording files lock")
		}
		e.Store.metaMu.Unlock()
	}
	d, err := e.Get(KindProject, "project", 0)
	if err != nil || !observed || len(d.Links) != 1 || d.Links[0].ID != recID {
		t.Fatalf("linked recording %+v %v", d, err)
	}
}
