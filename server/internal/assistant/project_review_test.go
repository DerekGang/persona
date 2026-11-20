package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

const reviewTestModel = "gpt-6.2-sol"

var reviewTestTime = time.Date(2099, 10, 15, 12, 0, 0, 0, time.UTC)
var reviewSource = SessionRef{MachineID: "box", SessionID: "source"}
var reviewTarget = SessionRef{MachineID: "box", SessionID: "review"}

func reviewConfig() projectReviewConfig {
	return projectReviewConfig{
		Enabled: true, Target: reviewTarget, Model: reviewTestModel,
		Projects: []string{"alpha"}, AppInputs: true,
	}
}

func reviewTestService(t *testing.T, store *db.DB) (*Service, *fakeHub) {
	t.Helper()
	s, f := newTaskServiceOn(t, store)
	s.projectReview = &projectReview{projectReviewConfig: reviewConfig(), db: store, wake: make(chan struct{}, 1)}
	return s, f
}

func TestProjectReviewManagerRestart(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("PERSONA_TEST_PROJECT_REVIEW_CAPTURE") == "1" {
		store, err := db.Open(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"), db.ManagerSet)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		s, _ := reviewTestService(t, store)
		todo := reviewNewTask(t, s, "alpha", "todo_alpha", reviewSource)
		a := proto.Session{MachineID: "box", SessionID: "source", UpdatedAt: reviewTestTime, Status: proto.StatusWorking}
		if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{SessionRef: reviewSource, MessageID: "owner-1", At: reviewTestTime}); err != nil {
			t.Fatal(err)
		}
		if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{SessionRef: reviewTarget, MessageID: "loop"}); err != nil {
			t.Fatal(err)
		}
		turn := []proto.Item{{ID: "finished", Kind: "turn_duration", TS: reviewTestTime, TurnDuration: &proto.TurnDuration{TurnID: "turn-1"}}}
		end, named := turnEndOf(turn)
		s.takeTurn(ctx, todo, a, turn, end, named, reviewTestTime.Add(-time.Minute), turnDone)
		s.takeTurn(ctx, todo, a, turn, end, named, reviewTestTime.Add(-time.Minute), turnDone)

		a.UpdatedAt = reviewTestTime.Add(time.Minute)
		a.Status = proto.StatusBlocked
		s.projectReviewSession(ctx, a, proto.StatusWorking)
		a.UpdatedAt = a.UpdatedAt.Add(time.Second)
		s.projectReviewSession(ctx, a, proto.StatusBlocked)

		return
	}

	store := todoTestDB(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(exe, "-test.run=^TestProjectReviewManagerRestart$")
	command.Env = append(os.Environ(), "PERSONA_TEST_PROJECT_REVIEW_CAPTURE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("capture process failed: %v\n%s", err, output)
	}
	s, f := reviewTestService(t, store)
	a := proto.Session{MachineID: "box", SessionID: "source", UpdatedAt: reviewTestTime.Add(time.Minute + 2*time.Second),
		Status: proto.StatusWorking, State: proto.StateWorking}
	s.projectReviewSession(ctx, a, "") // first live row after the producer process was restarted
	todo, ok, err := s.todo(ctx, "todo_alpha")
	if err != nil || !ok {
		t.Fatalf("new dispatch did not survive restart: %v %v", ok, err)
	}
	todo.Supervision = &Supervision{ExpectMinutes: 1}
	agents := []proto.Session{a}
	signals := watchSignals(todo, agents, a.UpdatedAt.Add(24*time.Hour))
	if len(signals) != 1 || signals[0].Kind != sigStalled {
		t.Fatalf("no watchdog evidence: %+v", signals)
	}
	s.projectReviewStall(ctx, signals[0], agents)
	s.projectReviewStall(ctx, signals[0], agents)
	target, _ := json.Marshal(reviewTarget)
	ready := proto.Session{SessionID: "review", Status: proto.StatusIdle, Agent: "codex", Model: reviewTestModel}
	f.sessions["box"] = []proto.Session{ready}
	advance := func(now time.Time) {
		t.Helper()
		if _, err := s.projectReviewStep(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"owner_input", "execution_completed", "waiting_for_owner", "stalled"}
	for i, kind := range want {
		e, ok, err := store.NextProjectReview(ctx, string(target))
		if err != nil || !ok || !strings.Contains(string(e.Event), `"kind": "`+kind+`"`) {
			t.Fatalf("#%d: want %s, got %+v %v", i, kind, e, err)
		}
		now := e.RetryAt.Add(time.Second)
		if i == 0 {
			f.sessions["box"][0].Model = "wrong-model"
			advance(now)
			retry, _, _ := store.NextProjectReview(ctx, string(target))
			if retry.ID != e.ID || retry.Attempts != 1 || len(f.sentOnce) != 0 {
				t.Fatalf("model mismatch advanced queue: %+v, sends=%d", retry, len(f.sentOnce))
			}
			f.sessions["box"][0].Model = reviewTestModel
			now = retry.RetryAt.Add(time.Second)
			f.failSendOnce = map[string]error{"box/review": errors.New("node disconnected")}
			advance(now)
			retry, _, _ = store.NextProjectReview(ctx, string(target))
			if retry.ID != e.ID || retry.Attempts != 2 || len(f.sentOnce) != 0 {
				t.Fatalf("failed delivery advanced queue: %+v", retry)
			}
			f.failSendOnce = nil
			now = retry.RetryAt.Add(time.Second)
		}
		advance(now)
		if len(f.sentOnce) != i+1 {
			t.Fatalf("send count=%d, want %d", len(f.sentOnce), i+1)
		}
		sent := f.sentOnce[i]
		if sent["session_id"] != "review" || sent["client_msg_id"] != e.ID || !strings.Contains(sent["text"].(string), "alpha") {
			t.Fatalf("invalid delivery: %+v", sent)
		}
		seq := len(f.items) + 1
		f.items = append(f.items, proto.Item{Seq: seq, Role: "user", Kind: proto.KindText, ClientMsgID: e.ID},
			proto.Item{Seq: seq + 1, Role: "assistant", Kind: proto.KindText, Text: "wrong receipt", Model: reviewTestModel},
			proto.Item{Seq: seq + 2, Kind: "turn_duration", ID: "review-finished"})
		f.itemsPage = 2
		retry, _, _ := store.NextProjectReview(ctx, string(target))
		advance(retry.RetryAt.Add(time.Second))
		if head, ok, _ := store.NextProjectReview(ctx, string(target)); !ok || head.ID != e.ID {
			t.Fatal("transport success or arbitrary output settled delivery")
		}
		f.items[seq].Text = "PROJECT_REVIEW_RECEIPT " + e.ID + " no_change"
		f.items = append(f.items, proto.Item{Seq: seq + 3, Role: "user", Kind: proto.KindText, ClientMsgID: "later"},
			proto.Item{Seq: seq + 4, Role: "assistant", Kind: proto.KindText, Text: "another turn", Model: "other-model"})
		// The sender and receipt reader are different Service instances sharing only the durable database and node.
		s = newServiceOn(t, f, store)
		s.projectReview = &projectReview{projectReviewConfig: reviewConfig(), db: store, wake: make(chan struct{}, 1)}
		f.sessions["box"][0].Model, f.sessions["box"][0].Status = "other-model", proto.StatusWorking
		retry, _, _ = store.NextProjectReview(ctx, string(target))
		advance(retry.RetryAt.Add(time.Second))
		f.sessions["box"][0] = ready
	}
	if _, ok, err := store.NextProjectReview(ctx, string(target)); err != nil || ok {
		t.Fatalf("late delivery after replay: %v %v", ok, err)
	}
	if tsk, ok := s.todoNow(todo.ID); !ok || tsk.DoneAt != nil {
		t.Fatalf("delivery changed acceptance: %+v", tsk)
	}
	t.Log("capture subprocess exited; dynamic task state restored; four receipts matched across pages after service restart and a newer turn; transport success alone held the queue")
}

func reviewNewTask(t *testing.T, s *Service, project, id string, agent SessionRef) Todo {
	t.Helper()
	ctx := context.Background()
	task, err := s.createTaskTodoAs(ctx, id, id, "", "main", "plan-test", project, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, task.ID, agent.MachineID, agent.SessionID, "", "", "codex"); err != nil {
		t.Fatal(err)
	}
	task, _ = s.todoNow(task.ID)
	return task
}

func TestProjectReviewDynamicScope(t *testing.T) {
	ctx := context.Background()
	store := todoTestDB(t)
	s, f := reviewTestService(t, store)
	target, _ := json.Marshal(reviewTarget)
	f.sessions["box"] = []proto.Session{{SessionID: reviewTarget.SessionID, Status: proto.StatusIdle, Agent: "codex", Model: reviewTestModel}}

	alpha := reviewNewTask(t, s, "alpha", "todo_dynamic", reviewSource)
	beta := SessionRef{MachineID: "box", SessionID: "beta"}
	reviewNewTask(t, s, "beta", "todo_beta", beta)
	for i, ref := range []SessionRef{reviewTarget, {MachineID: "box", SessionID: "main"}, beta} {
		if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{
			SessionRef: ref, MessageID: fmt.Sprintf("msg-%d", i), At: reviewTestTime,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{
		SessionRef: reviewSource, MessageID: "multi-project-input", At: reviewTestTime, Unscoped: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{
		SessionRef: reviewSource, MessageID: "captured-before-assignment", At: alpha.Agents[0].LinkedAt.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	row := proto.Session{MachineID: reviewSource.MachineID, SessionID: reviewSource.SessionID}
	s.projectReviewTurn(ctx, alpha, row, turnClose{Key: "old-turn", At: reviewTestTime}, turnDone)
	// The author withdraws alpha after it was queued. No previously authorized refs may leave the manager.
	s.projectReview.Projects = nil
	for i := 0; i < 5; i++ {
		next, ok, err := store.NextProjectReview(ctx, string(target))
		if err != nil || !ok {
			t.Fatalf("review-only event #%d: %v %v", i, ok, err)
		}
		if i < 4 && !strings.Contains(string(next.Event), `"project": ""`) {
			t.Fatalf("unscoped input received a project at capture: %s", next.Event)
		}
		if _, err := s.projectReviewStep(ctx, next.RetryAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		msg := f.sentOnce[i]["text"].(string)
		if strings.Contains(msg, "todo_") || strings.Contains(msg, "old-turn") || strings.Contains(msg, "msg-") ||
			strings.Contains(msg, "multi-project-input") || strings.Contains(msg, "captured-before-assignment") ||
			!strings.Contains(msg, next.ID+" triage_only") {
			t.Fatalf("unauthorized facts in review-only prompt: %s", msg)
		}
		if err := store.SettleProjectReview(ctx, next.ID, "test_drained"); err != nil {
			t.Fatal(err)
		}
	}

	s.projectReview.Projects = []string{"alpha"}
	if err := s.releaseStoppedAgents(ctx, alpha.ID, alpha.Agents); err != nil {
		t.Fatal(err)
	}
	replacement := SessionRef{MachineID: "box", SessionID: "replacement"}
	if err := s.linkTodoAgent(ctx, alpha.ID, replacement.MachineID, replacement.SessionID, "", "", "codex"); err != nil {
		t.Fatal(err)
	}
	alpha, _ = s.todoNow(alpha.ID)
	s.projectReviewTurn(ctx, alpha, row, turnClose{Key: "released-agent", At: reviewTestTime}, turnDone)
	row.SessionID = replacement.SessionID
	s.projectReviewTurn(ctx, alpha, row, turnClose{Key: "replacement-done", At: reviewTestTime}, turnDone)
	next, ok, err := store.NextProjectReview(ctx, string(target))
	if err != nil || !ok || !strings.Contains(string(next.Event), "replacement-done") || !strings.Contains(string(next.Event), alpha.ID) {
		t.Fatalf("replacement's event did not retain project attribution: %+v %v", next, err)
	}
	if err := store.SettleProjectReview(ctx, next.ID, "test_drained"); err != nil {
		t.Fatal(err)
	}
	if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{
		SessionRef: replacement, MessageID: "continued-task", At: reviewTestTime,
	}); err != nil {
		t.Fatal(err)
	}
	next, ok, err = store.NextProjectReview(ctx, string(target))
	if err != nil || !ok || !strings.Contains(string(next.Event), "continued-task") || !strings.Contains(string(next.Event), alpha.ID) {
		t.Fatalf("continuation on replacement did not follow the task ledger: %+v %v", next, err)
	}
	if err := store.SettleProjectReview(ctx, next.ID, "test_drained"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.NextProjectReview(ctx, string(target)); err != nil || ok {
		t.Fatalf("unexpected extra event: %v %v", ok, err)
	}
	looseAgent := SessionRef{MachineID: "box", SessionID: "unassigned"}
	loose := reviewNewTask(t, s, "", "todo_unassigned", looseAgent)
	looseRow := proto.Session{MachineID: looseAgent.MachineID, SessionID: looseAgent.SessionID}
	end := turnClose{Key: "unassigned-done", At: reviewTestTime}
	s.projectReviewTurn(ctx, loose, looseRow, end, turnDone)
	if _, ok, err := store.NextProjectReview(ctx, string(target)); err != nil || ok {
		t.Fatalf("unassigned task reported without opt-in: %v %v", ok, err)
	}
	s.projectReview.ReviewUnassigned = true
	s.projectReviewTurn(ctx, loose, looseRow, end, turnDone)
	next, ok, err = store.NextProjectReview(ctx, string(target))
	if err != nil || !ok || !strings.Contains(string(next.Event), "unassigned-done") {
		t.Fatalf("opted-in unassigned task did not produce triage: %s %v %v", next.Event, ok, err)
	}
	if _, err := s.projectReviewStep(ctx, next.RetryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	msg := f.sentOnce[len(f.sentOnce)-1]["text"].(string)
	if strings.Contains(msg, loose.ID) || strings.Contains(msg, end.Key) || !strings.Contains(msg, next.ID+" triage_only") {
		t.Fatalf("unassigned task leaked to triage: %s", msg)
	}
	if err := store.SettleProjectReview(ctx, next.ID, "test_drained"); err != nil {
		t.Fatal(err)
	}
	t.Log("main, unrelated project, revoked binding and opted-in unassigned task arrived as opaque triage; continuation and replacement inherited their authorized task")
}

func TestProjectReviewRejectsInvalidConfig(t *testing.T) {
	c := reviewConfig()
	c.Projects = []string{"alpha project"}
	if err := c.validate(); err == nil {
		t.Fatal("invalid project reference accepted")
	}
	c.Enabled = false
	if err := c.validate(); err != nil {
		t.Fatalf("disabled configuration: %v", err)
	}
}

func TestProjectReviewPairedFeishuOwnerInput(t *testing.T) {
	ctx := context.Background()
	store := todoTestDB(t)
	s, f := reviewTestService(t, store)
	s.projectReview.FeishuOwnerInputs = true
	status := FeishuStatus{Enabled: true, Ready: true, Owner: "ou_paired", Chats: []string{"oc_bound"}}
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) { return status, nil })
	s.rememberBotThread("bot-review", botTarget{Kind: targetAgent, MachineID: reviewTarget.MachineID, SessionID: reviewTarget.SessionID}, "Review")
	for _, tc := range []struct{ id, sender, senderType, chat, parent, from string }{
		{"om_owner", "ou_paired", "user", "oc_bound", "", ""},
		{"om_owner", "ou_paired", "user", "oc_bound", "", ""},
		{"om_other", "ou_guest", "user", "oc_bound", "", ""},
		{"om_unbound", "ou_paired", "user", "oc_other", "", ""},
		{"om_bot", "ou_paired", "app", "oc_bound", "", ""},
		{"om_elsewhere", "ou_paired", "user", "oc_bound", "", FeishuFromElsewhere},
		{"om_self", "ou_paired", "user", "oc_bound", "bot-review", ""},
	} {
		// Suppress unrelated bot replies in this test. The production input path still sees the redelivery.
		if _, err := s.claimBotMessage(tc.id); err != nil {
			t.Fatal(err)
		}
		raw := json.RawMessage(fmt.Sprintf(`{"sender":{"sender_type":%q,"sender_id":{"open_id":%q}},"message":{"message_id":%q,"chat_id":%q,"parent_id":%q,"chat_type":"p2p","message_type":"text","content":"private original"}}`,
			tc.senderType, tc.sender, tc.id, tc.chat, tc.parent))
		if err := s.FeishuMessage(ctx, FeishuMessage{Event: raw, From: tc.from}); err != nil {
			t.Fatal(err)
		}
	}
	target, _ := json.Marshal(reviewTarget)
	event, ok, err := store.NextProjectReview(ctx, string(target))
	if err != nil || !ok || !strings.Contains(string(event.Event), `"reference": "feishu:om_owner"`) ||
		!strings.Contains(string(event.Event), `"project": ""`) || strings.Contains(string(event.Event), "private original") {
		t.Fatalf("want an opaque owner event: %s %v %v", event.Event, ok, err)
	}
	if err := store.SettleProjectReview(ctx, event.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.NextProjectReview(ctx, string(target)); err != nil || ok {
		t.Fatalf("non-owner, unbound, self, or duplicate Feishu input reached the queue: %v %v", ok, err)
	}
	t.Log("paired owner handover arrived once; other sender, bot, unbound chat, elsewhere, and review target excluded")
}

// The hub queues an input with its own clock, and a new Agent's first prompt before the to-do link.
// Neither a later task update nor that ordering may take an authorized input out of its project.
func TestProjectReviewOwnerInputAssignmentEpoch(t *testing.T) {
	ctx := context.Background()
	store := todoTestDB(t)
	s, _ := reviewTestService(t, store)
	target, _ := json.Marshal(reviewTarget)
	alpha := reviewNewTask(t, s, "alpha", "todo_epoch", reviewSource)
	linked := alpha.Agents[0].LinkedAt
	notes := "a later edit"
	if _, err := s.editTodo(ctx, alpha.ID, db.TodoEdit{Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if updated, _ := s.todoNow(alpha.ID); !linked.Before(updated.UpdatedAt) {
		t.Fatalf("fixture: the edit did not move UpdatedAt past the link: %v %v", linked, updated.UpdatedAt)
	}
	cases := []struct {
		id     string
		at     time.Time
		scoped bool
	}{
		{"first-agent:" + reviewSource.SessionID, linked.Add(-time.Second), true},
		{"after-link-before-edit", linked, true},
		{"before-link", linked.Add(-time.Second), false},
		{"first-agent:another-session", linked.Add(-time.Second), false},
		{"no-time", time.Time{}, false},
	}
	for _, c := range cases {
		if err := s.projectReviewOwnerInput(ctx, assistantwire.ProjectReviewOwnerInput{SessionRef: reviewSource, MessageID: c.id, At: c.at}); err != nil {
			t.Fatal(err)
		}
		next, ok, err := store.NextProjectReview(ctx, string(target))
		if err != nil || !ok || !strings.Contains(string(next.Event), c.id) {
			t.Fatalf("%s: not queued: %s %v %v", c.id, next.Event, ok, err)
		}
		if got := strings.Contains(string(next.Event), alpha.ID); got != c.scoped {
			t.Errorf("%s: scoped=%v, want %v: %s", c.id, got, c.scoped, next.Event)
		}
		if err := store.SettleProjectReview(ctx, next.ID, "test_drained"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectReviewOutcomeTriageOnlyIsReviewOnly(t *testing.T) {
	cases := []struct {
		reply      string
		reviewOnly bool
		want       string
	}{
		{"done\nPROJECT_REVIEW_RECEIPT id-1 triage_only", true, "triage_only"},
		{"PROJECT_REVIEW_RECEIPT id-1 triage_only", false, ""},
		{"PROJECT_REVIEW_RECEIPT id-1 facts_recorded", false, "facts_recorded"},
		{"PROJECT_REVIEW_RECEIPT id-1 facts_recorded", true, ""},
		{"PROJECT_REVIEW_RECEIPT id-2 no_change", false, ""},
	}
	for _, c := range cases {
		if got := projectReviewOutcome(c.reply, "id-1", c.reviewOnly); got != c.want {
			t.Errorf("%q reviewOnly=%v: got %q, want %q", c.reply, c.reviewOnly, got, c.want)
		}
	}
}
