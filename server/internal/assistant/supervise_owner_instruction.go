package assistant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/proto"
)

// The owner can keep instructing an Agent after Persona handed it a task. The runtime only knows the turn that was
// running when the Agent stops, so an instruction that starts no new turn can disappear from a completion judgment.
// Keep the latest instruction and the latest Persona assignment per linked session, then require the instruction,
// when it is newer than the assignment, to be followed by a settled work report before a bare supervisor acceptance.

const (
	ownerInstructionExcerptMax = 200
	ownerReportTurns           = 6
)

// OwnerInstruction is the short, verified excerpt of an owner's message to one Agent session.
type OwnerInstruction struct {
	At          time.Time `json:"at"`
	Source      string    `json:"source"` // app or terminal
	ClientMsgID string    `json:"client_msg_id,omitempty"`
	Excerpt     string    `json:"excerpt"`
}

// OwnerSessionWork keeps the two independent clocks for one linked session.
type OwnerSessionWork struct {
	MachineID   string            `json:"machine_id"`
	SessionID   string            `json:"session_id"`
	AssignedAt  time.Time         `json:"assigned_at,omitzero"`
	Instruction *OwnerInstruction `json:"instruction,omitempty"`
}

func (v *Supervision) ownerSessionWork(machine, session string) *OwnerSessionWork {
	for i := range v.OwnerWork {
		if v.OwnerWork[i].MachineID == machine && v.OwnerWork[i].SessionID == session {
			return &v.OwnerWork[i]
		}
	}
	return nil
}

func (v *Supervision) recordOwnerInstruction(machine, session, source, clientMsgID, text string, at time.Time) bool {
	excerpt := clip(oneLine(strings.TrimSpace(text)), ownerInstructionExcerptMax)
	if machine == "" || session == "" || excerpt == "" || at.IsZero() {
		return false
	}
	switch source {
	case assistantwire.OwnerInputApp, assistantwire.OwnerInputTerminal:
	default:
		return false
	}
	at = at.UTC()
	work := v.ownerSessionWork(machine, session)
	if work == nil {
		v.OwnerWork = append(v.OwnerWork, OwnerSessionWork{MachineID: machine, SessionID: session})
		work = &v.OwnerWork[len(v.OwnerWork)-1]
	}
	if work.Instruction != nil && work.Instruction.At.After(at) {
		return false
	}
	if work.Instruction != nil && work.Instruction.At.Equal(at) &&
		work.Instruction.Source == source && work.Instruction.ClientMsgID == clientMsgID && work.Instruction.Excerpt == excerpt {
		return false
	}
	work.Instruction = &OwnerInstruction{At: at, Source: source, ClientMsgID: clientMsgID, Excerpt: excerpt}
	return true
}

func (v *Supervision) recordAssignment(machine, session string, at time.Time) bool {
	if machine == "" || session == "" || at.IsZero() {
		return false
	}
	at = at.UTC()
	work := v.ownerSessionWork(machine, session)
	if work == nil {
		v.OwnerWork = append(v.OwnerWork, OwnerSessionWork{MachineID: machine, SessionID: session, AssignedAt: at})
		return true
	}
	if !work.AssignedAt.IsZero() && !at.After(work.AssignedAt) {
		return false
	}
	work.AssignedAt = at
	return true
}

func (v *Supervision) recordAssignments(agents []TodoAgent, at time.Time) bool {
	changed := false
	for _, a := range agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		changed = v.recordAssignment(a.MachineID, a.SessionID, at) || changed
	}
	return changed
}

// updateOwnerWork writes only the private owner-work evidence. It deliberately leaves Supervision.UpdatedAt and the
// pending-review clock alone: an owner's message or a delivered assignment is not a review.
func (s *Service) updateOwnerWork(ctx context.Context, id string, change func(*Supervision) bool) error {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	b := &s.supervise
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	t, ok := s.todoNow(id)
	if !ok || t.Supervision == nil {
		return nil
	}
	v := t.Supervision.clone()
	if !change(v) {
		return nil
	}
	if st := s.supervisionDB(); st != nil && s.todoDB() != nil {
		if err := st.PutTodoSupervision(ctx, v.row(id)); err != nil {
			return err
		}
	}
	s.todos.mu.Lock()
	if cur := s.todos.todos[id]; cur != nil {
		next := v.clone()
		if cur.Supervision != nil {
			next.Progress = cur.Supervision.clone().Progress
		}
		cur.Supervision = next
	}
	s.todos.mu.Unlock()
	s.todosChanged(ctx)
	return nil
}

func (s *Service) noteOwnerInstruction(ctx context.Context, ref SessionRef, text, source, clientMsgID string, at time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	t, ok := s.taskOf(ctx, ref.MachineID, ref.SessionID)
	if !ok || t.Supervision == nil {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var linkedAt time.Time
	for _, a := range t.Agents {
		if a.MachineID == ref.MachineID && a.SessionID == ref.SessionID {
			linkedAt = a.LinkedAt
			break
		}
	}
	if err := s.updateOwnerWork(ctx, t.ID, func(v *Supervision) bool {
		changed := !linkedAt.IsZero() && v.recordAssignment(ref.MachineID, ref.SessionID, linkedAt)
		return v.recordOwnerInstruction(ref.MachineID, ref.SessionID, source, clientMsgID, text, at) || changed
	}); err != nil {
		s.logf("task %s: owner instruction: %v", t.ID, err)
	}
}

func (s *Service) notePersonaAssignment(ctx context.Context, machine, session string, at time.Time) {
	t, ok := s.taskOf(ctx, machine, session)
	if !ok || t.Supervision == nil {
		return
	}
	if err := s.updateOwnerWork(ctx, t.ID, func(v *Supervision) bool {
		return v.recordAssignment(machine, session, at)
	}); err != nil {
		s.logf("task %s: Persona assignment: %v", t.ID, err)
	}
}

// ownerInstructionBlock refuses a bare acceptance while any current linked session has an owner instruction newer
// than Persona's latest assignment to it and no later settled work report. A clean closeout after the instruction is
// the report from that round. An instruction folded into new completion criteria advances the assignment, so the
// existing per-criterion guard evaluates it instead.
func (s *Service) ownerInstructionBlock(ctx context.Context, t Todo) (matched, why string) {
	if cur, ok := s.todoNow(t.ID); ok {
		t = cur
	}
	if t.Supervision == nil {
		return "", ""
	}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		work := t.Supervision.ownerSessionWork(a.MachineID, a.SessionID)
		if work == nil || work.Instruction == nil {
			continue
		}
		assigned := work.AssignedAt
		if assigned.IsZero() || a.LinkedAt.After(assigned) {
			assigned = a.LinkedAt
		}
		instruction := *work.Instruction
		if !instruction.At.After(assigned) {
			continue
		}
		reason := s.ownerInstructionReport(ctx, a.MachineID, a.SessionID, instruction)
		if reason == "" {
			continue
		}
		return "owner_instruction_newer", fmt.Sprintf(
			"owner instruction %q (%s, %s) is newer than Persona's last assignment to that Agent and remains unresolved: %s",
			instruction.Excerpt, instruction.Source, instruction.At.UTC().Format(time.RFC3339), reason)
	}
	return "", ""
}

// ownerInstructionReport returns why the instruction is unresolved, or "" after a later settled report.
func (s *Service) ownerInstructionReport(ctx context.Context, machine, session string, instruction OwnerInstruction) string {
	var page struct {
		Items []proto.Item `json:"items"`
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{
		"session_id": session, "turns": ownerReportTurns, "lazy_images": true,
	}, &page); err != nil {
		return "its transcript could not be read to verify a later work report"
	}
	instructionSeq := 0
	if instruction.ClientMsgID != "" {
		for _, it := range page.Items {
			if it.Role == "user" && it.ClientMsgID == instruction.ClientMsgID {
				instructionSeq = it.Seq
			}
		}
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		it := page.Items[i]
		if it.Role != "assistant" || it.Kind != proto.KindText || strings.TrimSpace(it.Text) == "" {
			continue
		}
		if instructionSeq > 0 {
			if it.Seq <= instructionSeq {
				continue
			}
		} else if it.TS.IsZero() || it.TS.Before(instruction.At) {
			continue
		}
		c, _, ok := closeout.Parse(it.Text)
		if !ok {
			return "the latest reply after the instruction has no work report"
		}
		switch {
		case c.Status != proto.CloseoutDone:
			return fmt.Sprintf("the latest work report is %q, not done", c.Status)
		case strings.TrimSpace(c.Waiting) != "":
			return fmt.Sprintf("the later work report still has Waiting: %s", clip(oneLine(c.Waiting), 160))
		case strings.TrimSpace(c.Missing) != "":
			return fmt.Sprintf("the later work report still has Missing: %s", clip(oneLine(c.Missing), 160))
		default:
			return ""
		}
	}
	return "no assistant reply or work report followed the instruction"
}
