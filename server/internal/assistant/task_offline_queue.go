package assistant

import (
	"context"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

const (
	offlineTaskWaitKey = "assistant-tasks.offline_dispatch_waiting"
	offlineTaskMaxWait = 24 * time.Hour
)

var errOfflineTaskHandled = errors.New("task already handled before its offline-queued start")

// The waiting entry lives in the existing task plan queue. The launch operation id is
// committed before sending, and a successful start can be linked again without a second send.
type taskOfflineWait struct {
	MachineID string `json:"machine_id"`
	OpID      string `json:"op_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Unclear   bool   `json:"unclear,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
}

func (t PlanTask) claimedOfflineStart() bool {
	return t.Queue != nil && t.Queue.Offline != nil && t.Queue.Claimed
}

func (s *Service) holdOfflineTask(ctx context.Context, t PlanTask, retry bool, err error) bool {
	offline := errors.Is(err, errMachineOffline) || errors.Is(err, apierr.ErrOffline)
	if !offline || t.RequestGate != "" || t.claimedGatewayStart() || t.backendWorkspace() || startResultUnknown(err) || nodeMayHaveTaken(err) {
		return false
	}
	machine, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return false
	}
	now := time.Now().UTC()
	q := TaskQueue{
		Key: offlineTaskWaitKey, QueuedAt: now, NextAt: now, Attempt: 1, Retry: retry,
		Reason:  i18n.T(s.lang(ctx), offlineTaskWaitKey, i18n.Args{"name": firstNonEmpty(machine.Name, machine.ID)}),
		Offline: &taskOfflineWait{MachineID: machine.ID},
	}
	if t.claimedOfflineStart() {
		if t.Queue.Offline.MachineID != machine.ID || t.Queue.Offline.SessionID != "" {
			return false
		}
		q.QueuedAt, q.Retry, q.Attempt = t.Queue.QueuedAt, t.Queue.Retry, t.Queue.Attempt+1
	}
	return s.replaceTaskQueue(ctx, t, &q, queueCAS)
}

func (s *Service) retryOfflineTask(ctx context.Context, plan *TaskPlan, index int, task PlanTask, now time.Time) {
	task.planID, task.planIndex = plan.ID, index
	queue := *task.Queue
	if queue.Claimed && !queue.Offline.Expired {
		s.reconcileOfflineTask(ctx, task, now)
		return
	}
	if now.Before(queue.NextAt) {
		return
	}
	ready, err := s.queuedTaskReadyAt(ctx, task, queue.QueuedAt)
	if err != nil {
		s.logf("task %s: check offline dispatch: %v", task.TodoID, err)
		return
	}
	if !ready {
		s.replaceTaskQueue(ctx, task, nil, queueCAS)
		return
	}
	machine, online := s.device(ctx, queue.Offline.MachineID)
	expired := !now.Before(queue.QueuedAt.Add(offlineTaskMaxWait))
	if !expired && (!online || !machine.Reachable) {
		return
	}
	queued := *queue.Offline
	queued.OpID = newStartSessionID()
	queue.Offline = &queued
	queue.Claimed = true
	queue.NextAt = now.Add(taskStartTimeout + taskFailTimeout)
	if expired {
		queued.Expired = true
		queued.OpID = ""
		queue.NextAt = now.Add(taskFailTimeout)
	}
	if !s.replaceTaskQueue(ctx, task, &queue, queueUnstartedTask) {
		return
	}
	task.Queue = &queue
	if expired {
		s.failQueuedStart(ctx, plan, index, task, &queue)
		return
	}
	s.startPlanTaskAsync(plan.ID, plan.Conversation, index, task, queue.Retry, nil)
}

func (s *Service) checkClaimedOfflineStart(ctx context.Context, task PlanTask) error {
	if !task.claimedOfflineStart() {
		return nil
	}
	ready, err := s.queuedTaskReadyAt(ctx, task, task.Queue.QueuedAt)
	if err != nil {
		return err
	}
	if !ready {
		return errOfflineTaskHandled
	}
	return nil
}

func (s *Service) rememberOfflineTaskSession(ctx context.Context, task *PlanTask, mid, sid string) {
	if !task.claimedOfflineStart() || mid != task.Queue.Offline.MachineID || sid == "" {
		return
	}
	queue := *task.Queue
	receipt := *queue.Offline
	receipt.SessionID, receipt.Agent = sid, task.startedAgent
	queue.Offline = &receipt
	if s.replaceTaskQueue(ctx, *task, &queue, queueCAS) {
		task.Queue = &queue
	}
}

func (s *Service) reconcileOfflineTask(ctx context.Context, task PlanTask, now time.Time) {
	queue := *task.Queue
	receipt := *queue.Offline
	if receipt.Unclear || now.Before(queue.NextAt) {
		return
	}
	if receipt.SessionID == "" {
		machine, ok := s.device(ctx, receipt.MachineID)
		if !ok || !machine.Reachable {
			return
		}
		var status proto.StartStatus
		err := s.call(ctx, receipt.MachineID, proto.MethodStartStatus, proto.StartStatusParams{OpID: receipt.OpID}, &status)
		var remote *rpc.Error
		if err != nil && (!errors.As(err, &remote) || remote.Code != "method_not_found") || status.State == proto.StartStatusPending {
			return
		}
		if status.State != proto.StartStatusStarted || status.SessionID == "" {
			receipt.Unclear = true
			queue.Offline = &receipt
			queue.Reason = i18n.T(s.lang(ctx), "assistant-tasks.request_unconfirmed")
			s.replaceTaskQueue(ctx, task, &queue, queueCAS)
			return
		}
		s.rememberOfflineTaskSession(ctx, &task, receipt.MachineID, status.SessionID)
		receipt.SessionID = status.SessionID
	}
	if todo, ok := s.todoNow(task.TodoID); ok {
		for _, agent := range todo.Agents {
			if agent.MachineID == receipt.MachineID && agent.SessionID == receipt.SessionID {
				s.replaceTaskQueue(ctx, task, nil, queueCAS)
				return
			}
		}
	}
	task.startedAgent = receipt.Agent
	err := s.linkPlanAgent(ctx, task, receipt.MachineID, receipt.SessionID, false)
	if errors.Is(err, db.ErrTodoCompleted) {
		err = s.linkPlanAgent(ctx, task, receipt.MachineID, receipt.SessionID, true)
	}
	if err != nil {
		s.logf("task %s: link offline dispatch receipt: %v", task.TodoID, err)
		return
	}
	s.replaceTaskQueue(ctx, task, nil, queueCAS)
}
