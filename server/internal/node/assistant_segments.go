package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type segmentStarts struct {
	mu   sync.Mutex
	seen map[string]bool
}

// A positive persisted receipt can reconcile a lost manager reply. Absence is
// deliberately unknown after a node restart: it never authorizes a second start.
func (n *Node) segmentReceipt(operation string) (string, bool) {
	n.state.mu.Lock()
	defer n.state.mu.Unlock()
	for id, st := range n.state.Sessions {
		if st.SegmentOperation == operation {
			return id, true
		}
	}
	return "", false
}
func (n *Node) assistantSegment(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("invalid segment parameters")
	}
	if method == "assistant.segment.input" {
		id, _ := p["session_id"].(string)
		client, _ := p["client_msg_id"].(string)
		accepted := false
		n.echoes.mu.Lock()
		for _, r := range n.echoes.recs[id] {
			if r.ClientMsgID == client && r.Accepted {
				accepted = true
				break
			}
		}
		n.echoes.mu.Unlock()
		return map[string]bool{"accepted": accepted}, nil
	}
	operation, _ := p["operation_id"].(string)
	launcher, _ := p["launcher"].(string)
	if method == "assistant.segment.check" {
		agent, err := n.assistantAgent(ctx, launcher)
		if err != nil {
			return nil, err
		}
		policy := agent == "claude" || agent == "codex"
		if h := n.assistantRuntime(launcher); h != nil {
			h.mu.Lock()
			agent = h.hello.Agent
			if agent == "" {
				agent = h.hello.Runtime
			}
			h.mu.Unlock()
			policy = h.supervisionPolicy()
		}
		return map[string]any{"engine": agent, "supervision_policy": policy, "prepare": true}, nil
	}
	if operation == "" || len(operation) > 128 {
		return nil, errors.New("invalid segment operation")
	}
	if id, ok := n.segmentReceipt(operation); ok {
		return map[string]any{"session_id": id, "prepared": true}, nil
	}
	if method == "assistant.segment.receipt" {
		return map[string]any{"unknown": true}, nil
	}
	n.segmentStarts.mu.Lock()
	if n.segmentStarts.seen == nil {
		n.segmentStarts.seen = map[string]bool{}
	}
	if n.segmentStarts.seen[operation] {
		n.segmentStarts.mu.Unlock()
		return map[string]any{"unknown": true}, nil
	}
	n.segmentStarts.seen[operation] = true
	n.segmentStarts.mu.Unlock()
	// The preparation API accepts no input and no caller-supplied policy. Policy is
	// supplied by the manager with the first actual input after epoch activation.
	for _, key := range []string{"text", "prompt", "images", "supervision_instructions", "session_id", "new_session_id", "client_msg_id"} {
		delete(p, key)
	}
	delete(p, "operation_id")
	data, _ := json.Marshal(p)
	result, err := n.handleMethod(ctx, "assistant.send", data)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	var reply struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(out, &reply) != nil || strings.TrimSpace(reply.SessionID) == "" {
		return nil, errors.New("segment start returned no identity")
	}
	state, _ := n.state.get(reply.SessionID)
	state.SegmentOperation = operation
	n.state.put(reply.SessionID, state)
	return map[string]any{"session_id": reply.SessionID, "prepared": true}, nil
}
