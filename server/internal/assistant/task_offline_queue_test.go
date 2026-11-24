package assistant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

func setTestComputerOnline(f *fakeHub, online bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[0].Online, f.devices[0].Reachable = online, online
}

func TestOfflineTaskWaitsThenLinksOneAgent(t *testing.T) {
	ctx := context.Background()
	s, f, resources := admissionHub(t)
	busyHealthy(resources)
	setTestComputerOnline(f, false)
	plan, todo := queuedStart(t, s, "busy")
	queue := plan.Tasks[0].Queue
	if queue == nil || queue.Offline == nil || queue.Offline.MachineID != "busy" || todo.DoneAt != nil {
		t.Fatalf("offline dispatch did not wait: queue=%+v todo=%+v", queue, todo)
	}
	cards, err := s.taskCards(ctx, []FeedItem{{ID: "plan", Kind: feedPlan, Ref: plan.ID, At: time.Now()}}, map[string]*TaskPlan{plan.ID: plan})
	if err != nil || len(cards) != 1 || cards[0].State != "waiting" || cards[0].Reason != queue.Reason {
		t.Fatalf("offline dispatch card: %+v, %v", cards, err)
	}
	view := s.todoProjectionOf(ctx, []listedTodo{formatTodo(todo)}, time.Now()).views[0]
	if view.Section != assistantwire.TodoListSectionWaiting || view.DispatchWait != queue.Reason || !view.DispatchCancel {
		t.Fatalf("offline dispatch view: %+v", view)
	}
	s.sweepAdmissionQueue(ctx)
	if starts := taskStartMachines(f); len(starts) != 0 {
		t.Fatalf("start while offline: %v", starts)
	}
	setTestComputerOnline(f, true)
	s.sweepAdmissionQueue(ctx)
	s.sweepAdmissionQueue(ctx)
	waitFor(t, "the queued Agent to link", func() bool { return len(todoNow(s, todo.ID).Agents) == 1 })
	waitFor(t, "the offline queue to clear", func() bool {
		after, err := s.loadPlan(ctx, plan.ID)
		return err == nil && after.Tasks[0].Queue == nil
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 1 || f.starts[0]["op_id"] == "" || f.starts[0]["op_id"] == nil {
		t.Fatalf("expected one start with an operation id: %+v", f.starts)
	}
}

func TestOfflineTaskExpiresOrOwnerEndsItBeforeReconnect(t *testing.T) {
	for _, ended := range []bool{false, true} {
		name := "expires"
		if ended {
			name = "owner ends"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, f, resources := admissionHub(t)
			busyHealthy(resources)
			setTestComputerOnline(f, false)
			plan, todo := queuedStart(t, s, "busy")
			if plan.Tasks[0].Queue == nil {
				t.Fatal("the dispatch must wait while offline")
			}
			if ended {
				uri := "/api/v1/todos/" + todo.ID + "/start/cancel"
				readOnly := httptest.NewRequest(http.MethodPost, uri, nil)
				readOnly.Header.Set(RoleHeader, "read")
				readOnlyAnswer := httptest.NewRecorder()
				s.ServeHTTP(readOnlyAnswer, readOnly)
				if readOnlyAnswer.Code != http.StatusForbidden {
					t.Fatalf("read-only owner action: %d %s", readOnlyAnswer.Code, readOnlyAnswer.Body)
				}
				for range 2 {
					answer := httptest.NewRecorder()
					s.ServeHTTP(answer, httptest.NewRequest(http.MethodPost, uri, nil))
					if answer.Code != http.StatusOK || !todoNow(s, todo.ID).Cancelled() {
						t.Fatalf("cancel queued start: %d %s", answer.Code, answer.Body)
					}
				}
			} else if err := s.patchPlanTask(ctx, plan.ID, 0, func(task *PlanTask) {
				task.Queue.QueuedAt = time.Now().Add(-offlineTaskMaxWait - time.Minute)
			}); err != nil {
				t.Fatal(err)
			}
			setTestComputerOnline(f, true)
			s.sweepAdmissionQueue(ctx)
			after, err := s.loadPlan(ctx, plan.ID)
			if err != nil || after.Tasks[0].Queue != nil || todoNow(s, todo.ID).DoneAt == nil {
				t.Fatalf("closed offline dispatch: %+v, %v", after, err)
			}
			if starts := taskStartMachines(f); len(starts) != 0 {
				t.Fatalf("closed dispatch reached the computer: %v", starts)
			}
		})
	}
}

func TestOfflineTaskRestoresAStoredStartReceiptWithoutAnotherStart(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	before, originalHub, originalResources := admissionHubOn(t, database)
	busyHealthy(originalResources)
	setTestComputerOnline(originalHub, false)
	plan, todo := queuedStart(t, before, "busy")
	if plan.Tasks[0].Queue == nil {
		t.Fatal("the dispatch must wait before persisting the start")
	}
	if err := before.patchPlanTask(ctx, plan.ID, 0, func(task *PlanTask) {
		task.Queue.Claimed = true
		task.Queue.Offline.OpID = "accepted-start"
		task.Queue.Offline.SessionID = "confirmed-session"
		task.Queue.NextAt = time.Now().Add(-time.Second)
	}); err != nil {
		t.Fatal(err)
	}
	after, hub, _ := admissionHubOn(t, database)
	after.sweepAdmissionQueue(ctx)
	linked := todoNow(after, todo.ID)
	if len(linked.Agents) != 1 || linked.Agents[0].SessionID != "confirmed-session" {
		t.Fatalf("manager did not link the recorded session: %+v", linked.Agents)
	}
	if starts := taskStartMachines(hub); len(starts) != 0 {
		t.Fatalf("manager sent a second start after recovery: %v", starts)
	}
}
