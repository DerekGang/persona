package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

func existingInboxArgs(name string, todo Todo, item string) map[string]any {
	args := map[string]any{"inbox_item": item}
	if name == "task_scope" {
		args["todo_id"], args["change"], args["why"] = todo.ID, "Check the additional reported case", "The owner supplied another example"
	} else {
		args["machine"], args["session_id"], args["text"] = todo.Agents[0].MachineID, todo.Agents[0].SessionID, "Check the additional reported case"
		if name == "agent_send(task)" {
			args["task"] = map[string]any{"title": todo.Title, "done_when": []string{"The original and additional cases are verified"}}
		}
	}
	return args
}

func existingInboxCall(t *testing.T, s *Service, name string, args map[string]any) (string, error) {
	t.Helper()
	if name == "agent_send(task)" {
		name = "agent_send"
	}
	return runJSON(t, s, "conv1", name, args)
}

func assertExistingInboxLink(t *testing.T, s *Service, id string, todo Todo) {
	t.Helper()
	raw, ok := s.InboxItemJSON(t.Context(), id)
	var item InboxItem
	if !ok || json.Unmarshal(raw, &item) != nil {
		t.Fatal("Inbox detail is unavailable", string(raw))
	}
	if item.Agent == nil || item.Agent.TodoID != todo.ID || item.Agent.SessionID != "" {
		t.Fatal("task backlink missing, or the processing conversation was replaced", item.Agent)
	}
	agentLinks := 0
	for _, entry := range item.Chain {
		if entry.Kind == ChainAgent && inboxChainSession(entry, todo.Agents[0].MachineID, todo.Agents[0].SessionID) {
			agentLinks++
		}
	}
	if agentLinks != 1 || !item.open() {
		t.Fatalf("Inbox detail needs exactly one live Agent link and must remain open: %d, %+v", agentLinks, item)
	}
}

func TestExistingTaskInboxHandoffBindsSendAndScopeWithoutTaskPlan(t *testing.T) {
	for _, name := range []string{"agent_send", "agent_send(task)", "task_scope"} {
		t.Run(name, func(t *testing.T) {
			st := todoTestDB(t)
			s, f := newQuietServiceOn(t, st)
			todo := agentTask(t, s, f)
			const original = "example:original-task-source"
			if _, err := s.editTodo(t.Context(), todo.ID, db.TodoEdit{InboxItem: ptr(original)}); err != nil {
				t.Fatal(err)
			}
			it := taskInboxItem(t, s, "example:follow-up", "server", "conv1")
			// Nothing is inferred from the prompt; reload the manager's persisted handoff.
			next, nf := newQuietServiceOn(t, st)
			secretary(nf)
			if _, err := existingInboxCall(t, next, name, existingInboxArgs(name, todo, "")); err != nil {
				t.Fatal(err)
			}
			assertExistingInboxLink(t, next, it.ID, todo)
			if got := todoNow(next, todo.ID); got.InboxItem != original || len(got.Agents) != 1 || got.DoneAt != nil {
				t.Fatal("follow-up replaced the original task or source", got)
			}
			rows, err := st.ListTodos(t.Context())
			if err != nil || len(rows) != 1 || len(nf.started()) != 0 {
				t.Fatal("linking created another to-do or Agent", rows, err, nf.started())
			}
			again, _ := newQuietServiceOn(t, st)
			if err := again.inboxReady(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertExistingInboxLink(t, again, it.ID, todo)
			// Recording the same task/session twice adds no duplicate chain entries.
			source := PlanTask{}
			if err := again.planInboxSource(t.Context(), "server", "conv1", &source); err != nil {
				t.Fatal(err)
			}
			if err := again.ensureTodosLoaded(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := again.linkExistingTaskInbox(t.Context(), "conv1", source, todo.ID,
				[]SessionRef{{MachineID: todo.Agents[0].MachineID, SessionID: todo.Agents[0].SessionID}}); err != nil {
				t.Fatal(err)
			}
			assertExistingInboxLink(t, again, it.ID, todo)
		})
	}
}

func TestExistingTaskInboxHandoffAmbiguityRequiresExplicitSourceBeforeSideEffects(t *testing.T) {
	for _, name := range []string{"agent_send", "agent_send(task)", "task_scope"} {
		t.Run(name, func(t *testing.T) {
			s, f := newQuietService(t)
			todo := agentTask(t, s, f)
			a := taskInboxItem(t, s, "example:follow-up-a", "server", "conv1")
			b := taskInboxItem(t, s, "example:follow-up-b", "server", "conv1")
			before := len(sentToAgent(f))
			brief, err := s.ensureTaskBrief(t.Context(), todo)
			if err != nil {
				t.Fatal(err)
			}
			result, err := existingInboxCall(t, s, name, existingInboxArgs(name, todo, ""))
			if err == nil || !strings.Contains(err.Error(), "inbox_item") || !strings.Contains(err.Error(), a.ID) || !strings.Contains(err.Error(), b.ID) {
				t.Fatal("ambiguous handoff was not refused", result, err)
			}
			after, _ := s.briefOfTodo(todo.ID)
			if len(sentToAgent(f)) != before || len(after.Log) != len(brief.Log) || len(after.Criteria) != len(brief.Criteria) {
				t.Fatal("ambiguous call sent a message or changed task scope")
			}
			if _, err := existingInboxCall(t, s, name, existingInboxArgs(name, todo, a.ID)); err != nil {
				t.Fatal(err)
			}
			assertExistingInboxLink(t, s, a.ID, todo)
			s.inbox.mu.Lock()
			otherLinked := b.Agent != nil && b.Agent.TodoID != ""
			s.inbox.mu.Unlock()
			if otherLinked {
				t.Fatal("explicit selection linked the other item")
			}
		})
	}
}

func TestExistingTaskInboxHandoffDoesNotCrossContextOrPolicy(t *testing.T) {
	for _, name := range []string{"agent_send", "task_scope"} {
		for _, kind := range []string{"other conversation", "other machine", "filed", "bot", "already linked"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				s, f := newQuietService(t)
				todo := agentTask(t, s, f)
				machine, conversation := "server", "conv1"
				if kind == "other conversation" {
					conversation = "another-conversation"
				}
				if kind == "other machine" {
					machine = "another-machine"
				}
				it := taskInboxItem(t, s, "example:policy", machine, conversation)
				s.inbox.mu.Lock()
				switch kind {
				case "filed":
					it.DoneAt = new(time.Now())
				case "bot":
					it.Kind = KindBot
				case "already linked":
					it.Agent = &InboxAgent{State: AgentSkipped, TodoID: "another-task"}
				}
				s.inbox.mu.Unlock()
				before := len(sentToAgent(f))
				if _, err := existingInboxCall(t, s, name, existingInboxArgs(name, todo, it.ID)); err == nil {
					t.Fatal("explicit invalid handoff accepted")
				}
				if len(sentToAgent(f)) != before {
					t.Fatal("invalid handoff sent a message")
				}
				if kind != "already linked" {
					if _, err := existingInboxCall(t, s, name, existingInboxArgs(name, todo, "")); err != nil {
						t.Fatal("unrelated or ineligible items blocked ordinary work", err)
					}
					s.inbox.mu.Lock()
					linked := it.Agent != nil && it.Agent.TodoID != ""
					s.inbox.mu.Unlock()
					if linked {
						t.Fatal("ordinary work adopted an unrelated or ineligible item")
					}
				}
			})
		}
	}
}

func TestExistingTaskInboxHandoffRechecksBeforeLinkingAndKeepsProcessingConversation(t *testing.T) {
	s, f := newQuietService(t)
	todo := agentTask(t, s, f)
	it := taskInboxItem(t, s, "example:changed", "server", "conv1")
	source := PlanTask{}
	if err := s.planInboxSource(t.Context(), "server", "conv1", &source); err != nil {
		t.Fatal(err)
	}
	s.inbox.mu.Lock()
	it.DoneAt = new(time.Now())
	s.inbox.mu.Unlock()
	ref := SessionRef{MachineID: todo.Agents[0].MachineID, SessionID: todo.Agents[0].SessionID}
	if err := s.linkExistingTaskInbox(t.Context(), "conv1", source, todo.ID, []SessionRef{ref}); err == nil {
		t.Fatal("filed item linked after preflight")
	}
	s.inbox.mu.Lock()
	it.DoneAt = nil
	it.Agent = &InboxAgent{State: AgentRunning, MachineID: "server", SessionID: "processing-conversation"}
	s.inbox.mu.Unlock()
	if err := s.linkExistingTaskInbox(context.Background(), "conv1", source, todo.ID, []SessionRef{ref}); err != nil {
		t.Fatal(err)
	}
	s.inbox.mu.Lock()
	defer s.inbox.mu.Unlock()
	if it.Agent.TodoID != todo.ID || it.Agent.SessionID != "processing-conversation" || it.Agent.MachineID != "server" {
		t.Fatal("backlink overwrote the Inbox processing conversation", it.Agent)
	}
}

func TestExistingTaskInboxHandoffFailedSendDoesNotClaimAnAgent(t *testing.T) {
	s, f := newQuietService(t)
	todo := agentTask(t, s, f)
	it := taskInboxItem(t, s, "example:failed-send", "server", "conv1")
	f.failSend = errors.New("the node refused the message")
	if _, err := existingInboxCall(t, s, "agent_send", existingInboxArgs("agent_send", todo, "")); err == nil {
		t.Fatal("failed send reported success")
	}
	s.inbox.mu.Lock()
	defer s.inbox.mu.Unlock()
	if it.Agent != nil && it.Agent.TodoID != "" {
		t.Fatal("failed send claimed task ownership", it.Agent)
	}
	for _, entry := range it.Chain {
		if entry.Kind == ChainAgent {
			t.Fatal("failed send claimed an Agent", entry)
		}
	}
}

func TestExistingTaskInboxHandoffSaveFailureReportsPartialSuccessAndRollsBack(t *testing.T) {
	for _, name := range []string{"agent_send", "task_scope"} {
		t.Run(name, func(t *testing.T) {
			st := &taskInboxFailStore{DB: todoTestDB(t)}
			s, f := newQuietServiceOn(t, st)
			todo := agentTask(t, s, f)
			it := taskInboxItem(t, s, "example:failed-save", "server", "conv1")
			before := len(sentToAgent(f))
			st.failInbox.Store(true)
			_, err := existingInboxCall(t, s, name, existingInboxArgs(name, todo, ""))
			if err == nil || !strings.Contains(err.Error(), "Inbox link was not saved") {
				t.Fatal("failed association save reported full success", err)
			}
			if name == "agent_send" && (len(sentToAgent(f)) != before+1 || !strings.Contains(err.Error(), "do not resend")) {
				t.Fatal("partial delivery was hidden, risking a duplicate send", err)
			}
			s.inbox.mu.Lock()
			linked := it.Agent != nil && it.Agent.TodoID != ""
			chain := append([]InboxChainEntry(nil), it.Chain...)
			s.inbox.mu.Unlock()
			if linked {
				t.Fatal("failed save left a success backlink in memory")
			}
			for _, entry := range chain {
				if entry.Kind == ChainAgent {
					t.Fatal("failed save left an Agent entry in memory")
				}
			}
			st.failInbox.Store(false)
			next, _ := newQuietServiceOn(t, st)
			if err := next.inboxReady(t.Context()); err != nil {
				t.Fatal(err)
			}
			next.inbox.mu.Lock()
			defer next.inbox.mu.Unlock()
			got := next.inbox.items[it.ID]
			if got == nil || got.Agent != nil && got.Agent.TodoID != "" {
				t.Fatal("failed save leaked an association into durable state", got)
			}
		})
	}
}
