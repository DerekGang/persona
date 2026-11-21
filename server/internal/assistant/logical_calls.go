package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/conversationlog"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

func logicalRefusal(ctx context.Context, code string) error {
	wire := CodeBadRequest
	if code == "busy" {
		wire = assistantwire.CodeConversationBusy
	}
	return &rpc.Error{Code: wire, Message: i18n.T(i18n.From(ctx), "assistant-conversation_segments."+code), Data: json.RawMessage(`{"delivered":"none"}`)}
}

// This is the single raw source boundary. Its callers have already resolved and
// fenced public identity; it never re-applies a physical session's model or role.
func (s *Service) logicalPhysical(ctx context.Context, machine, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, logicalSourceKey{}, true)
	if d := s.physicalDevices(); d != nil {
		return d.CallNode(ctx, machine, method, raw)
	}
	return s.hub.DeviceCall(ctx, DeviceCall{Device: machine, Method: method, Params: raw})
}

func logicalSendMethod(method string) bool {
	return method == DeviceAssistantSend || method == DeviceSessionSend || method == DeviceSessionSendOnce
}

func (s *Service) logicalCall(ctx context.Context, machine, method string, raw json.RawMessage) (json.RawMessage, bool, error) {
	if ctx.Value(logicalSourceKey{}) != nil || ctx.Value(historySourceKey{}) != nil {
		return nil, false, nil
	}
	st, ok := s.store().(logicalConversationStore)
	if !ok {
		return nil, false, nil
	}
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return nil, false, nil
	}
	id, _ := p["session_id"].(string)
	if id == "" {
		return nil, false, nil
	}
	c, err := st.LogicalConversation(ctx, machine, id)
	if err != nil {
		return nil, true, err
	}
	if c == nil {
		return nil, false, nil
	}
	if c.Deleted {
		return nil, true, db.ErrConversationDeleted
	}
	if c.MachineID != machine || c.SessionID != id {
		return nil, true, logicalRefusal(ctx, "private_segment")
	}
	if method == DeviceSessionItems || method == "session.item" || method == "session.image" || method == DeviceAssistantHandoff {
		out, err := s.logicalHistoryCall(ctx, st, c, method, raw)
		return out, true, err
	}
	active, ok := c.Active()
	if !ok {
		return nil, true, logicalRefusal(ctx, "unavailable")
	}
	if method == "session.watch" || method == "session.unwatch" {
		s.logical.mu.Lock()
		if s.logical.watches == nil {
			s.logical.watches = map[[2]string]json.RawMessage{}
		}
		if method == "session.watch" {
			s.logical.watches[[2]string{machine, id}] = append(json.RawMessage(nil), raw...)
		} else {
			delete(s.logical.watches, [2]string{machine, id})
		}
		s.logical.mu.Unlock()
	}
	var out json.RawMessage
	err = st.WithConversationEpoch(ctx, machine, id, c.Epoch, func(current *db.LogicalConversation) error {
		active, ok = current.Active()
		if !ok {
			return logicalRefusal(ctx, "unavailable")
		}
		if logicalSendMethod(method) {
			if err := s.putSupervisionInstructions(ctx, machine, id, p); err != nil {
				return err
			}
			// Queues are used throughout a switch, including its first target turn.
			if current.Switch != nil {
				client, _ := p["client_msg_id"].(string)
				if client == "" {
					return logicalRefusal(ctx, "message_id_required")
				}
				body, e := json.Marshal(struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}{method, p})
				if e != nil {
					return e
				}
				if e = st.EnqueueConversationInput(ctx, machine, id, client, current.Epoch, body); e != nil {
					return e
				}
				out, e = json.Marshal(map[string]any{"ok": true, "queued": true, "session_id": id, "client_msg_id": client})
				s.historyWake()
				return e
			}
		}
		physical := cloneLogicalParams(p, active)
		if method == "session.watch" {
			for _, key := range []string{"since_seq", "after_seq"} {
				if n, ok := physical[key].(float64); ok {
					physical[key] = max(0, int(n)-active.Offset)
				}
			}
			physical["history"] = active.SourceHistory
		}
		var e error
		out, e = s.logicalPhysical(ctx, active.MachineID, method, physical)
		return e
	})
	if err != nil {
		return nil, true, err
	}
	// Control replies may contain the physical session row. Only the public root
	// is returned to the application; source references stay inside the manager.
	return publicLogicalReply(out, c), true, nil
}

func cloneLogicalParams(p map[string]any, active conversationlog.Segment) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	out["session_id"] = active.SessionID
	return out
}

func publicLogicalReply(raw json.RawMessage, c *db.LogicalConversation) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	if _, ok := obj["session_id"]; ok {
		obj["session_id"], _ = json.Marshal(c.SessionID)
	}
	if _, ok := obj["machine_id"]; ok {
		obj["machine_id"], _ = json.Marshal(c.MachineID)
	}
	if row, ok := obj["session"]; ok {
		obj["session"] = publicLogicalReply(row, c)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// Called only after the node/hub authenticated the actual physical caller.
// An alias is never itself evidence that a retired process holds today's role.
func (s *Service) logicalCaller(ctx context.Context, machine, session string) (string, string, *db.LogicalConversation, error) {
	st, ok := s.store().(logicalConversationStore)
	if !ok {
		return machine, session, nil, nil
	}
	c, err := st.LogicalConversation(ctx, machine, session)
	if err != nil || c == nil {
		return machine, session, c, err
	}
	a, ok := c.Active()
	if c.Deleted || !ok || a.MachineID != machine || a.SessionID != session {
		return "", "", c, logicalRefusal(ctx, "retired")
	}
	return c.MachineID, c.SessionID, c, nil
}

func logicalNotTaken(err error) bool {
	if err == nil {
		return false
	}
	if sendNotTaken(err) {
		return true
	}
	var re *rpc.Error
	if errors.As(err, &re) {
		return strings.Contains(string(re.Data), `"delivered":"none"`)
	}
	return false
}
