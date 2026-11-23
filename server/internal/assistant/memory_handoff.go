package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A dispatched task's first message carries the memory entries the task needs (#13532, scene 3, docs/assistant/
// health.md "Memory by scene"): general ones its words bear on, sensitive ones only when its own text was tagged with
// every one of their categories. A text so tagged is itself confirmed on the phone before it goes (confirmOutbound),
// so a sensitive entry never leaves without that. The to-do's brief lists the entries carried (a memory event).

// handOffMemory is what task t, handed to machine, carries of the ledger: entries for any executor that apply on
// machine, personal ones only on the owner's own computer, none on a work computer. The task's text is tagged only
// when a sensitive entry could go with it.
func (s *Service) handOffMemory(ctx context.Context, t PlanTask, machine string) assembled {
	if s.memoryEntrySrc() == nil || s.isWorkMachine(machine) {
		return assembled{}
	}
	text := strings.TrimSpace(t.Title + "\n" + t.Prompt)
	p := proto.AssistantEntriesParams{Scope: &proto.AssistantEntryScopeFilter{Executor: "any", Machine: []string{machine}}}
	entries, err := s.memoryEntriesFor(ctx, p, machine != "" && machine == s.settings(ctx).MachineID)
	if err != nil {
		s.logf("task %s: memory: %v", t.TodoID, err)
	}
	if len(entries) == 0 {
		return assembled{}
	}
	info := s.memoryEntryUses(ctx, entries)
	f := assembleFor{Role: assembleHandOff, Query: text}
	if slices.ContainsFunc(entries, func(e proto.AssistantEntry) bool { return info(e).sensitive() }) {
		origin := ""
		if todo, ok := s.todoNow(t.TodoID); ok {
			origin = todo.Origin
		}
		space, _ := s.conversationSpace(ctx, origin)
		u := s.exitUse(ctx, text, origin, space)
		f.Topic, f.TopicKnown = u.Categories, u.Known
	}
	return assembleEntries(entries, f, info)
}

// handOffMemoryContext is the context block of the entries task t carries to machine, empty when none; the to-do's
// brief notes which went.
func (s *Service) handOffMemoryContext(ctx context.Context, t PlanTask, machine string) string {
	a := s.handOffMemory(ctx, t, machine)
	if len(a.Entries) == 0 {
		return ""
	}
	s.noteHandOffMemory(ctx, t.TodoID, a)
	return s.renderMemoryEntries(ctx, a)
}

// renderMemoryEntries is the context block of a's entries, in the answer language.
func (s *Service) renderMemoryEntries(ctx context.Context, a assembled) string {
	d := prompts.MemoryEntries{Language: answerLanguage(s.hello(ctx).Locale)}
	for i, e := range a.Entries {
		d.Entries = append(d.Entries, prompts.MemoryEntry{N: i + 1, Text: contextText(e.Text)})
	}
	return prompts.RenderMemoryEntries(d)
}

// noteHandOffMemory writes into to-do todoID's brief which entries its first message carried, once per set: a start
// tried again with the same entries notes nothing more. A to-do with no brief yet keeps no note (logged).
func (s *Service) noteHandOffMemory(ctx context.Context, todoID string, a assembled) {
	brief, ok := s.briefOfTodo(todoID)
	if todoID == "" || !ok {
		s.logf("task %s: memory: %d entries carried, no brief to note them in", todoID, len(a.Entries))
		return
	}
	ids, sensitive := a.IDs(), slices.Clone(a.Sensitive)
	if _, _, err := s.changeBrief(ctx, brief.ID, func(tb *taskBrief) bool {
		for i := len(tb.st.Log) - 1; i >= 0; i-- {
			if e := tb.st.Log[i]; e.Kind == briefEventMemory && e.Todo == todoID {
				if slices.Equal(e.Entries, ids) {
					return false
				}
				break
			}
		}
		tb.st.Log = append(tb.st.Log, BriefEvent{At: time.Now().UTC(), Kind: briefEventMemory, Todo: todoID, Entries: ids,
			SensitiveEntries: sensitive})
		return true
	}); err != nil {
		s.logf("task %s: memory: note in brief: %v", todoID, err)
	}
}
