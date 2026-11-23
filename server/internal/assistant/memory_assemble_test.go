package assistant

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// memEntrySource is a ledger in memory for the assembler (memoryEntrySource): the entries oldest first, filtered by
// the personal boundary, executor and machine as the manager database does, paged by index.
type memEntrySource struct {
	entries []proto.AssistantEntry
	listed  int
}

func (m *memEntrySource) ListAssistantEntries(_ context.Context, p proto.AssistantEntriesParams, personal bool) (proto.AssistantEntriesResult, error) {
	m.listed++
	var match []proto.AssistantEntry
	for _, e := range m.entries {
		if e.Scope.Personal && !personal {
			continue
		}
		if f := p.Scope; f != nil {
			if f.Executor != "" && e.Scope.Executor != "any" && e.Scope.Executor != f.Executor {
				continue
			}
			if len(f.Machine) > 0 && len(e.Scope.Machine) > 0 && !slices.ContainsFunc(f.Machine, func(m string) bool { return slices.Contains(e.Scope.Machine, m) }) {
				continue
			}
		}
		match = append(match, e)
	}
	from, _ := strconv.Atoi(p.After)
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	out := proto.AssistantEntriesResult{Entries: []proto.AssistantEntry{}}
	if from < len(match) {
		to := min(from+limit, len(match))
		out.Entries = append(out.Entries, match[from:to]...)
		out.HasMore = to < len(match)
		out.After = strconv.Itoa(to)
	}
	return out, nil
}

// Fake entries, oldest first: a finance rule said in qa-finance (tagged finance, personal), a general personal entry
// from qa-plain (its conversation tagged with none), an entry from qa-plain whose conversation was never tagged, an
// entry from the health space, and a general entry of the main track any executor may use.
var (
	entryQAFinance = proto.AssistantEntry{ID: "entry-qa-finance", Type: "rule", Text: "qa: monthly budget totals, 记账按月汇总",
		OriginSpace: "qa-finance", Category: []string{categoryFinance}, Source: proto.AssistantEntrySource{Machine: "qa-machine", Session: "qa-finance-1"},
		Scope: proto.AssistantEntryScope{Executor: "any", Personal: true}, Status: "active"}
	entryQAPlain = proto.AssistantEntry{ID: "entry-qa-plain", Type: "preference", Text: "qa: release notes go out on Fridays",
		OriginSpace: "qa-plain", Source: proto.AssistantEntrySource{Machine: "qa-machine", Session: "qa-plain-1"},
		Scope: proto.AssistantEntryScope{Executor: "assistant", Personal: true}, Status: "active"}
	entryQAUntagged = proto.AssistantEntry{ID: "entry-qa-untagged", Type: "fact", Text: "qa: a placeholder fact",
		OriginSpace: "qa-plain", Source: proto.AssistantEntrySource{Machine: "qa-machine", Session: "qa-plain-2"},
		Scope: proto.AssistantEntryScope{Executor: "assistant", Personal: true}, Status: "active"}
	entryQAHealth = proto.AssistantEntry{ID: "entry-qa-health", Type: "fact", Text: "qa: a placeholder step goal",
		OriginSpace: SpaceHealth, Source: proto.AssistantEntrySource{Machine: "qa-machine", Session: "qa-health-1"},
		Scope: proto.AssistantEntryScope{Executor: "any", Personal: true}, Status: "active"}
	entryQAMain = proto.AssistantEntry{ID: "entry-qa-main", Type: "rule", Text: "qa: the monthly report uses the shared template",
		Source: proto.AssistantEntrySource{Machine: "qa-machine", Session: "qa-main"}, Scope: proto.AssistantEntryScope{Executor: "any"}, Status: "active"}
)

// memoryAssembleService is infoUseService with the fake ledger, the owner's computer qa-machine, and the conversations
// qa-main and qa-plain-1 tagged with no category (qa-plain-2 never tagged).
func memoryAssembleService(t *testing.T) (*Service, *fakeHub, *infoCategoryMemStore, *memEntrySource) {
	t.Helper()
	ctx := context.Background()
	s, f, st := infoUseService(t)
	setSettings(t, s, func(v *Settings) { v.MachineID = "qa-machine" })
	for _, sid := range []string{"qa-main", "qa-plain-1"} {
		if err := st.AddInfoCategories(ctx, db.InfoCategoryConversation, sid, []string{infoNoneTagged}, infoTaggedByModel, ""); err != nil {
			t.Fatal(err)
		}
	}
	src := &memEntrySource{entries: []proto.AssistantEntry{entryQAFinance, entryQAPlain, entryQAUntagged, entryQAHealth, entryQAMain}}
	s.memoryEntries = src
	return s, f, st, src
}

func entryIDs(entries []proto.AssistantEntry) string {
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

// How an entry is seen (memoryEntryUses): its own categories and its track's; one with none takes its conversation's
// when that was tagged, and is not known otherwise.
func TestMemoryEntryUses(t *testing.T) {
	ctx := context.Background()
	s, _, _, src := memoryAssembleService(t)
	info := s.memoryEntryUses(ctx, src.entries)
	for _, c := range []struct {
		e     proto.AssistantEntry
		cats  string
		known bool
	}{
		{entryQAFinance, "finance", true},
		{entryQAPlain, "", true},
		{entryQAUntagged, "", false},
		{entryQAHealth, "health", false},
		{entryQAMain, "", true},
		{proto.AssistantEntry{OriginSpace: "qa-unknown", Category: []string{categoryFinance}}, "finance", false},
		{proto.AssistantEntry{Category: []string{"qa-not-a-category"}}, "", false},
	} {
		u := info(c.e)
		if strings.Join(u.Categories, ",") != c.cats || u.Known != c.known || u.SourceTrack != c.e.OriginSpace {
			t.Errorf("%s: %+v, want categories %q known %v", c.e.ID, u, c.cats, c.known)
		}
	}
	// With no model for info.tag nothing is tagged: an entry carries its track's default categories.
	s.noInfoTag = true
	info = s.memoryEntryUses(ctx, src.entries)
	if u := info(entryQAUntagged); !u.Known || len(u.Categories) != 0 {
		t.Errorf("untagged, no tagging: %+v, want general", u)
	}
	if u := info(entryQAHealth); !u.Known || strings.Join(u.Categories, ",") != "health" {
		t.Errorf("health, no tagging: %+v, want health", u)
	}
}

// Acceptance (a): a sensitive entry from another track appears 0 times in the main conversation's standing channel,
// which carries the main track's entries and personal general ones from elsewhere; in its own track it is carried.
func TestMemoryStandingNeverCarriesSensitiveFromElsewhere(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := memoryAssembleService(t)
	a, err := s.standingMemory(ctx, "qa-main")
	if err != nil {
		t.Fatal(err)
	}
	if got := entryIDs(a.Entries); got != "entry-qa-plain,entry-qa-main" {
		t.Errorf("main standing = %s, want the personal general entry and the main track's", got)
	}
	if strings.Contains(entryIDs(a.Entries), entryQAFinance.ID) || len(a.Sensitive) != 0 {
		t.Errorf("main standing carries sensitive entries: %+v", a)
	}
	// Even when the main conversation was tagged finance: a standing channel never carries elsewhere's sensitive ones.
	if err := s.tagConversation(ctx, "qa-main", []string{categoryFinance}, infoTaggedByModel, ""); err != nil {
		t.Fatal(err)
	}
	a, _ = s.standingMemory(ctx, "qa-main")
	if strings.Contains(entryIDs(a.Entries), entryQAFinance.ID) {
		t.Errorf("main standing, tagged finance, carries %s", entryQAFinance.ID)
	}
	// In its own track (scene 1) it is carried; the main track's entry, finance by now and from elsewhere, is not.
	a, _ = s.standingMemory(ctx, "qa-finance-1")
	if got := entryIDs(a.Entries); got != "entry-qa-finance,entry-qa-plain" || !slices.Equal(a.Sensitive, []string{entryQAFinance.ID}) {
		t.Errorf("qa-finance standing = %s (sensitive %v)", got, a.Sensitive)
	}
}

// A standing channel keeps within its budget, newest first.
func TestMemoryStandingBudget(t *testing.T) {
	var entries []proto.AssistantEntry
	for i := range 4 {
		entries = append(entries, proto.AssistantEntry{ID: "entry-" + strconv.Itoa(i), Text: strings.Repeat("q", 600)})
	}
	a := assembleEntries(entries, assembleFor{Role: assembleStanding, SpaceKnown: true}, func(proto.AssistantEntry) infoUse { return infoUse{Known: true} })
	if got := entryIDs(a.Entries); got != "entry-2,entry-3" {
		t.Errorf("standing within %d bytes = %s, want the newest two", memoryStandingSpaceMax, got)
	}
}

// Acceptance (b): when the conversation's categories carry finance, a recall returns the finance entry; otherwise not.
// An entry whose categories are not known is never recalled elsewhere.
func TestMemoryRecallFollowsTheConversationsCategories(t *testing.T) {
	ctx := context.Background()
	s, _, _, src := memoryAssembleService(t)
	if got := entryIDs(s.recallMemory(ctx, "qa-main", src.entries).Entries); got != "entry-qa-plain,entry-qa-main" {
		t.Errorf("recall without finance = %s", got)
	}
	if err := s.tagConversation(ctx, "qa-main", []string{categoryFinance}, infoTaggedByModel, ""); err != nil {
		t.Fatal(err)
	}
	a := s.recallMemory(ctx, "qa-main", src.entries)
	// The main track's entry takes its conversation's categories (finance now): it is in its own track, so it stays.
	if got := entryIDs(a.Entries); got != "entry-qa-finance,entry-qa-plain,entry-qa-main" || !slices.Equal(a.Sensitive, []string{entryQAFinance.ID, entryQAMain.ID}) {
		t.Errorf("recall with finance = %s (sensitive %v)", got, a.Sensitive)
	}
	// While a turn of the conversation waits to be tagged its categories are not known: nothing sensitive is recalled.
	s.infoTagWaiting.set("qa-main", true)
	if got := entryIDs(s.recallMemory(ctx, "qa-main", src.entries).Entries); strings.Contains(got, entryQAFinance.ID) {
		t.Errorf("recall while a turn waits = %s", got)
	}
	s.infoTagWaiting.set("qa-main", false)
	// A general conversation of another track recalls its own track's entries, untagged ones included, and not the
	// main track's entry, finance by now through its conversation.
	if got := entryIDs(s.recallMemory(ctx, "qa-plain-1", src.entries).Entries); got != "entry-qa-plain,entry-qa-untagged" {
		t.Errorf("recall in qa-plain = %s", got)
	}
}

// Acceptance (c): a dispatched task's first message carries only the sensitive entries its text was tagged with, and
// the to-do's brief lists them; general entries go when the task's words bear on them.
func TestMemoryHandOffCarriesOnlyWhatTheTaskMatches(t *testing.T) {
	ctx := context.Background()
	s, f, _, _ := memoryAssembleService(t)
	if _, err := s.putTaskBrief(ctx, briefRecord{Goal: "qa: a placeholder goal"}, "todo-qa-1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.infoTag = `{"categories":["finance"]}`
	f.mu.Unlock()
	task := PlanTask{TodoID: "todo-qa-1", Title: "qa: monthly report", Prompt: "qa: prepare the monthly budget totals report"}
	text := s.handOffMemoryContext(ctx, task, "qa-machine")
	if !strings.Contains(text, entryQAFinance.Text) || !strings.Contains(text, entryQAMain.Text) {
		t.Errorf("hand-off tagged finance lacks the finance entry or the relevant general one:\n%s", text)
	}
	for _, e := range []proto.AssistantEntry{entryQAHealth, entryQAPlain, entryQAUntagged} {
		if strings.Contains(text, e.Text) {
			t.Errorf("hand-off carries %s:\n%s", e.ID, text)
		}
	}
	ids, sensitive := handOffMemoryOf(s, "todo-qa-1")
	if !slices.Equal(ids, []string{entryQAFinance.ID, entryQAMain.ID}) || !slices.Equal(sensitive, []string{entryQAFinance.ID}) {
		t.Errorf("the brief lists %v (sensitive %v)", ids, sensitive)
	}
	// A start tried again with the same entries notes nothing more.
	s.handOffMemoryContext(ctx, task, "qa-machine")
	if n := memoryEvents(s, "todo-qa-1"); n != 1 {
		t.Errorf("%d memory events after a retry, want 1", n)
	}

	// A task tagged health carries no finance entry, and no unknown health one either.
	f.mu.Lock()
	f.infoTag = `{"categories":["health"]}`
	f.mu.Unlock()
	a := s.handOffMemory(ctx, PlanTask{Title: "qa: monthly report", Prompt: "qa: prepare the monthly report on sleep"}, "qa-machine")
	if got := entryIDs(a.Entries); got != entryQAMain.ID || len(a.Sensitive) != 0 {
		t.Errorf("hand-off tagged health = %s (sensitive %v)", got, a.Sensitive)
	}
	// Another computer gets no personal entry; a task whose words bear on nothing gets nothing, and asks no model.
	f.mu.Lock()
	f.infoTag = `{"categories":["finance"]}`
	f.mu.Unlock()
	if got := entryIDs(s.handOffMemory(ctx, task, "qa-other").Entries); got != entryQAMain.ID {
		t.Errorf("hand-off to another computer = %s", got)
	}
	asked := len(f.infoTagAsked)
	s.memoryEntries = &memEntrySource{entries: []proto.AssistantEntry{entryQAMain}}
	if a := s.handOffMemory(ctx, PlanTask{Prompt: "qa: something unrelated"}, "qa-machine"); len(a.Entries) != 0 || len(f.infoTagAsked) != asked {
		t.Errorf("unrelated hand-off = %+v after %d more tagging calls", a, len(f.infoTagAsked)-asked)
	}
}

// Acceptance (d): the Inbox's helper context carries 0 sensitive entries, even ones the message is about.
func TestMemoryInboxCarriesNoSensitiveEntry(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := memoryAssembleService(t)
	a, err := s.inboxMemory(ctx, "qa: can you send the monthly budget totals report, 记账按月汇总")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Sensitive) != 0 || strings.Contains(entryIDs(a.Entries), entryQAFinance.ID) {
		t.Errorf("Inbox carries sensitive entries: %+v", a)
	}
	if got := entryIDs(a.Entries); got != entryQAMain.ID {
		t.Errorf("Inbox = %s, want the general entry the message bears on", got)
	}
}

// decisions_query is a recall: the finance decision from qa-finance reaches the main conversation only once it
// carries finance (scene 2).
func TestMemoryDecisionsQueryIsARecall(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := memoryAssembleService(t)
	entries := []proto.AssistantEntry{entryQAFinance, entryQAMain}
	if got := entryIDs(s.recallMemory(ctx, "qa-main", entries).Entries); got != entryQAMain.ID {
		t.Errorf("decisions in main = %s", got)
	}
	if got := s.recallMemory(ctx, "qa-main", nil).Entries; got != nil {
		t.Errorf("no entries = %v", got)
	}
}

// The user's own extension (trust owner, such as their memory extension) reads every track's memory, qa-finance's
// included; a third party's does not without a finance grant (scene 6).
func TestMemoryOwnerExtensionReadsEveryTrack(t *testing.T) {
	ctx := context.Background()
	st := &trustMemStore{infoCategoryMemStore: &infoCategoryMemStore{trackRegistryStore: &trackRegistryStore{memStore: &memStore{}, rows: []db.Track{
		{Slug: "qa-finance", Kind: trackKindSpace, Template: trackTemplateSpace, DefaultCategories: []string{categoryFinance}},
	}}}}
	s := newServiceOn(t, newFakeHub(), st)
	if err := st.SetExtensionTrust(ctx, "qa-memory", assistantwire.ExtensionTrustOwner, nil, "user"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.memorySpaces(ctx), "qa-finance") {
		t.Fatalf("memory spaces %v lack qa-finance", s.memorySpaces(ctx))
	}
	if s.extensionHides(s.withExtensionReader(ctx, "qa-memory"), "qa-finance") {
		t.Error("the owner's extension does not read qa-finance's memory")
	}
	if !s.extensionHides(s.withExtensionReader(ctx, "qa-third"), "qa-finance") {
		t.Error("a third party's extension reads qa-finance's memory")
	}
}

// The memory block a hand-off carries, in each language (#13532).
func TestGoldenMemoryHandOff(t *testing.T) {
	ctx := context.Background()
	a := assembled{Entries: []proto.AssistantEntry{{ID: "entry-1", Text: "qa: the release notes go out on Fridays"}, {ID: "entry-2", Text: "qa: 记账按月汇总"}}}
	for _, locale := range []string{"zh-Hans", "en", "ja"} {
		f := newFakeHub()
		f.hello.Locale = locale
		s := newTestService(t, f)
		golden(t, "memory_handoff."+locale, s.renderMemoryEntries(ctx, a))
	}
	if got := newTestService(t, newFakeHub()).renderMemoryEntries(ctx, assembled{}); got != "" {
		t.Errorf("no entries = %q", got)
	}
}

// handOffMemoryOf is the entries the latest memory event of to-do todoID's brief lists, and the sensitive ones.
func handOffMemoryOf(s *Service, todoID string) (ids, sensitive []string) {
	brief, ok := s.briefOfTodo(todoID)
	if !ok {
		return nil, nil
	}
	for i := len(brief.Log) - 1; i >= 0; i-- {
		if e := brief.Log[i]; e.Kind == briefEventMemory && e.Todo == todoID {
			return e.Entries, e.SensitiveEntries
		}
	}
	return nil, nil
}

func memoryEvents(s *Service, todoID string) int {
	brief, _ := s.briefOfTodo(todoID)
	n := 0
	for _, e := range brief.Log {
		if e.Kind == briefEventMemory && e.Todo == todoID {
			n++
		}
	}
	return n
}
