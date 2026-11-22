package assistant

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

const larkPersonTaskPrefix = "todo_lark_"

func validLarkPersonTaskID(id string) bool {
	return strings.HasPrefix(id, larkPersonTaskPrefix) && parkReplyID(id)
}

func validLarkPersonText(value string, max int) bool {
	return value != "" && len(value) <= max && !strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ParkLarkPersonTask creates or advances the real assistant-owned parked task after a proven delivery. The opaque
// instance is frozen before transport; this review's actual timestamp is written only after readback.
func (s *Service) ParkLarkPersonTask(ctx context.Context, request assistantwire.LarkPersonPark) (assistantwire.LarkPersonTask, error) {
	if !validLarkPersonTaskID(request.TaskID) || !parkReplyID(request.ParkInstance) ||
		!validLarkPersonText(request.Title, 200) || !validLarkPersonText(request.DisplayName, 200) || request.SentAt.IsZero() {
		return assistantwire.LarkPersonTask{}, errors.New("invalid external bot park")
	}
	s.tasks.reviews.acquire(request.TaskID)
	defer s.tasks.reviews.release(request.TaskID)
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	todo, ok := s.todoNow(request.TaskID)
	if !ok {
		var err error
		todo, err = s.createTaskTodoAs(ctx, request.TaskID, request.Title, "", "", "", "", "")
		if err != nil {
			return assistantwire.LarkPersonTask{}, err
		}
	}
	if todo.DoneAt != nil {
		return assistantwire.LarkPersonTask{}, errors.New("external bot task is already closed")
	}
	evidence := &ParkEvidence{Kind: "person_reply", Target: request.DisplayName, NextActor: "external",
		Wake: "The external collaborator replies in the exact bound topic", ParkInstance: request.ParkInstance,
		Contact: &ParkContact{Channel: "Feishu topic", URL: request.URL, Sender: "Feishu bot", SentAt: request.SentAt.UTC(),
			Request: "External task assignment", Status: "sent"}, Reply: &request.Watch}
	now := time.Now().UTC()
	parked, err := readPark("Waiting for the external collaborator's exact reply", "30d", parkSomeoneElse, evidence, now)
	if err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	if todo.Supervision != nil {
		for _, review := range todo.Supervision.Reviews {
			if review.Evidence == nil || review.Evidence.ParkInstance != request.ParkInstance {
				continue
			}
			if !reflect.DeepEqual(review.Evidence, evidence) {
				return assistantwire.LarkPersonTask{}, errors.New("external bot park instance is already bound")
			}
			return larkPersonTaskState(todo), nil
		}
	}
	_, _, err = s.updateSupervision(ctx, request.TaskID, func(v *Supervision) bool {
		v.State = reviewParked
		v.Waits = nil
		v.Reviews = append(v.Reviews, TaskReview{At: now, Verdict: reviewParked, Reason: parked.Reason,
			Until: parked.Until, WaitingOn: parked.WaitingOn, Parks: 1, Evidence: evidence})
		return true
	})
	if err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	todo, _ = s.todoNow(request.TaskID)
	return larkPersonTaskState(todo), nil
}

func (s *Service) LarkPersonTaskState(ctx context.Context, taskID string) (assistantwire.LarkPersonTask, error) {
	if !validLarkPersonTaskID(taskID) {
		return assistantwire.LarkPersonTask{}, errors.New("invalid external bot task")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	todo, ok := s.todoNow(taskID)
	if !ok {
		return assistantwire.LarkPersonTask{Lifecycle: "active"}, nil
	}
	return larkPersonTaskState(todo), nil
}

func larkPersonTaskState(todo Todo) assistantwire.LarkPersonTask {
	state := assistantwire.LarkPersonTask{Lifecycle: "active"}
	if todo.DoneAt != nil {
		state.Lifecycle = "completed"
		if todo.DoneBy == db.DoneByCancelled {
			state.Lifecycle = "abandoned"
		}
	}
	if todo.Supervision != nil && todo.Supervision.State == reviewPending && todo.DoneAt == nil {
		state.ReviewState = "pending_review"
	}
	return state
}

// CloseLarkPersonTask records only the user's explicit completion or abandonment. The Bots source releases the topic
// after this write and retries the release idempotently if its first attempt fails.
func (s *Service) CloseLarkPersonTask(ctx context.Context, request assistantwire.LarkPersonClose) (assistantwire.LarkPersonTask, error) {
	if !validLarkPersonTaskID(request.TaskID) || request.Action != "complete" && request.Action != "abandon" {
		return assistantwire.LarkPersonTask{}, errors.New("invalid external bot close")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	todo, ok := s.todoNow(request.TaskID)
	if !ok {
		return assistantwire.LarkPersonTask{}, errors.New("external bot task has not started")
	}
	if todo.DoneAt != nil {
		current := larkPersonTaskState(todo)
		if request.Action == "complete" && current.Lifecycle == "completed" || request.Action == "abandon" && current.Lifecycle == "abandoned" {
			return current, nil
		}
		return assistantwire.LarkPersonTask{}, errors.New("external bot task is already closed")
	}
	accepted := request.Action == "complete"
	reason := "External bot task abandoned by the user"
	if accepted {
		reason = "External bot task completed by the user"
	}
	// Record the outcome as the review's state first, as other closes do (task_leader.go): left "parked", the To-dos
	// page reads an abandoned task as accepted.
	verdict := reviewCancelled
	if accepted {
		verdict = reviewAccepted
	}
	if _, _, err := s.updateSupervision(ctx, request.TaskID, func(v *Supervision) bool { v.State = verdict; return true }); err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	if _, err := s.reviewDoneTodo(ctx, request.TaskID, reason, accepted); err != nil {
		return assistantwire.LarkPersonTask{}, err
	}
	todo, _ = s.todoNow(request.TaskID)
	return larkPersonTaskState(todo), nil
}
