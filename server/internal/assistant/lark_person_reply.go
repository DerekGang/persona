package assistant

import (
	"context"
	"reflect"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
)

// RecordLarkPersonReply is the narrow larkperson.ReplyRecorder implementation. The park instance is the exact parked
// review timestamp, which prevents a redelivered observation from waking a later park on the same task.
// A matching source receipt only enters pending_review; it is not owner consent and is never sent to an Agent.
func (s *Service) RecordLarkPersonReply(ctx context.Context, taskID, parkInstance string, observation connectorwire.LarkReplyObservation) (bool, error) {
	todo, ok := s.todoNow(taskID)
	if !ok || todo.DoneAt != nil {
		return false, nil
	}
	parked := todo.Supervision.parking()
	if parked == nil {
		if len(todo.Supervision.Reviews) == 0 {
			return false, nil
		}
		latest := &todo.Supervision.Reviews[len(todo.Supervision.Reviews)-1]
		if latest.Verdict != reviewParked || latest.At.UTC().Format(time.RFC3339Nano) != parkInstance ||
			!parkReplyBinding(latest.Evidence) || latest.Reply == nil {
			return false, nil
		}
		// A prior commit already persisted this exact receipt. Do not enqueue another review or supervisor signal.
		return reflect.DeepEqual(*latest.Reply, observation), nil
	}
	if parked.At.UTC().Format(time.RFC3339Nano) != parkInstance || !parkReplyMatches(parked, observation) {
		return false, nil
	}
	if err := s.applyParkReply(ctx, todo, observation); err != nil {
		return false, err
	}
	updated, ok := s.todoNow(taskID)
	if !ok || len(updated.Supervision.Reviews) == 0 {
		return false, nil
	}
	latest := &updated.Supervision.Reviews[len(updated.Supervision.Reviews)-1]
	return latest.Reply != nil && reflect.DeepEqual(*latest.Reply, observation), nil
}
