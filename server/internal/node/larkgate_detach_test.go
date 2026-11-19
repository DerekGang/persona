package node

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The real PreToolUse path returns promptly with an explicit non-execution
// result when the manager releases a detachable approval. No request reaches
// the gated API, and the card remains the one to continue later.
func TestLarkGatePendingResultEndsTheToolCallWithoutExecuting(t *testing.T) {
	r := newLarkEditRig(t, func(ix proto.Interaction) proto.Resolution {
		return proto.Resolution{Dismiss: true, Pending: true, ApprovalID: ix.ID,
			Message: "The approval is still pending. Nothing was executed. End this turn now; a later message will report the outcome."}
	})
	cwd := t.TempDir()
	const command = `ex POST '/conversations/F1/messages' '{"text":"wait for approval"}'`
	spec := runLarkEditHook(t, r, cwd, command, nil)
	if spec["permissionDecision"] != "deny" {
		t.Fatalf("pending hook decision = %#v, want no execution", spec)
	}
	reason, _ := spec["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "End this turn now") || !strings.Contains(reason, "Nothing was executed") {
		t.Fatalf("pending hook reason = %q", reason)
	}
	card := r.hub.lastCreated(t)
	if !card.Detachable || card.ID == "" {
		t.Fatalf("command-gate card did not opt into detaching: %#v", card)
	}
	if got := r.api.requests(); len(got) != 0 {
		t.Fatalf("the pending command reached the gated API: %+v", got)
	}
}
