package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// defaultApprovalMirrorTools limits the two new routes to operations that can
// change something. Read-only runtime tools are deliberately absent. Extension
// writes are admitted through ToolAccess below, so installing a connector does
// not require editing this list. An explicit approval_mirror_tools replaces the
// complete list and the extension fallback for new cards.
var defaultApprovalMirrorTools = []string{
	"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit",
	"agent_archive", "agent_interrupt", "agent_send", "agent_start", "agent_supervise",
	"cancel_followup", "conversation_send", "kb_update", "mail_send", "notify_user",
	"persona_settings", "schedule_followup", "schedule_routine", "supervision_finish",
	"task_plan", "task_review", "task_scope", "todo_complete", "todo_update",
}

func approvalMirrorToolName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "mcp__persona__")
}

func cleanApprovalMirrorTools(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = approvalMirrorToolName(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func approvalMirrorToolNames(v Settings) []string {
	if v.ApprovalMirrorTools != nil {
		return cleanApprovalMirrorTools(*v.ApprovalMirrorTools)
	}
	return slices.Clone(defaultApprovalMirrorTools)
}

func (s *Service) approvalMirrorToolAllowed(ctx context.Context, ix proto.Interaction) bool {
	v := s.settings(ctx)
	name := approvalMirrorToolName(ix.ToolName)
	if name != "" && slices.Contains(approvalMirrorToolNames(v), name) {
		return true
	}
	// A configured list is an exact replacement. With the default list, an
	// existing command-gate write route or an installed extension's own
	// read/write declaration decides instead.
	if v.ApprovalMirrorTools != nil {
		return false
	}
	for _, key := range ix.AlwaysKeys {
		if strings.HasPrefix(key, "lark:") {
			return true
		}
	}
	if name == "" || s.ext == nil {
		return false
	}
	known, readOnly := s.ext.ToolAccess(name)
	return known && !readOnly
}

// approvalMirrorMain controls display copies for new cards. Existing copies keep
// following their authoritative card until that card resolves.
func (s *Service) approvalMirrorMain(ctx context.Context) bool {
	return quietOn(s.settings(ctx).ApprovalMirrorMain)
}

// approvalMirrorID is stable across updates and reconnects of one occurrence,
// and distinct when a runtime later reuses the original ID for another card.
func approvalMirrorID(ix proto.Interaction) string {
	return proto.ApprovalMirrorID(ix.ID, ix.CreatedAt)
}

// assistantApprovalMirrorEligible is the boundary that persisted display intent
// may never bypass: even an existing copy belongs only to an eligible Assistant
// permission, never to an Agent, health, private or nested card.
func (s *Service) assistantApprovalMirrorEligible(ctx context.Context, ix proto.Interaction) bool {
	return ix.Kind == proto.IxPermission && !ix.Private && ix.SessionID != "" && ix.MirrorOf == "" &&
		!s.ixInHealth(ctx, ix) && s.ixInAssistant(ctx, ix)
}

// nonMainAssistantPermission is the narrow routing boundary shared by the main
// conversation copy and the bot's direct chat. Only owner-decision write tools
// opt in. Agent sessions, private cards, health conversations, questions and
// display copies themselves never opt in. Existing main copies bypass this
// new-card filter until their authoritative operation ends.
func (s *Service) nonMainAssistantPermission(ctx context.Context, ix proto.Interaction) bool {
	if !s.assistantApprovalMirrorEligible(ctx, ix) || !s.approvalMirrorToolAllowed(ctx, ix) {
		return false
	}
	v := s.settings(ctx)
	if v.Main == "" || ix.SessionID != v.Main {
		return true
	}
	mainMachine := s.confirmedMainMachine(ctx, v)
	return mainMachine != "" && ix.MachineID != mainMachine
}

// approvalMirrorHub reports whether this hub understands display-copy identity,
// validation, normalization and synchronized withdrawal. An older hub receives
// only the authoritative card and keeps its existing behavior.
func (s *Service) approvalMirrorHub(ctx context.Context) bool {
	hello, err := s.hub.Hello(ctx)
	return err == nil && hello.ApprovalMirrors
}

// mainApprovalMirror returns the display-only copy for ix. Only the hub keeps
// this copy; the manager's pending map, decision boundary and aggregate counts
// continue to contain just the authoritative interaction. MirrorMain preserves
// an already-created copy after the preference changes or either side restarts.
func (s *Service) mainApprovalMirror(ctx context.Context, ix proto.Interaction) (proto.Interaction, bool) {
	if !s.assistantApprovalMirrorEligible(ctx, ix) || !s.approvalMirrorHub(ctx) {
		return proto.Interaction{}, false
	}
	if !ix.MirrorMain && (!s.approvalMirrorMain(ctx) || !s.nonMainAssistantPermission(ctx, ix)) {
		return proto.Interaction{}, false
	}
	v := s.settings(ctx)
	mainMachine := s.confirmedMainMachine(ctx, v)
	if v.Main == "" || mainMachine == "" {
		return proto.Interaction{}, false
	}
	mirror := ix
	mirror.ID = approvalMirrorID(ix)
	mirror.MirrorOf = ix.ID
	mirror.MirrorMain = false
	mirror.MachineID = mainMachine
	mirror.SessionID = v.Main
	mirror.FoldedUnder, mirror.FoldedAs, mirror.FoldedSide = "", "", ""
	mirror.Muted = false
	return mirror, true
}

// sameApprovalOccurrence distinguishes a resumed card from a later operation
// whose runtime happened to reuse its ID. Ordinary cards use their creation
// timestamp. A detachable original replay may refresh it, so exact call bytes
// are its occurrence identity instead.
func sameApprovalOccurrence(a, b proto.Interaction) bool {
	if a.ID != b.ID || a.MachineID != b.MachineID || a.SessionID != b.SessionID {
		return false
	}
	if a.Detachable || b.Detachable {
		if !a.Detachable || !b.Detachable {
			return false
		}
		af, aerr := detachedFingerprint(a)
		bf, berr := detachedFingerprint(b)
		return aerr == nil && berr == nil && af == bf
	}
	return !a.CreatedAt.IsZero() && a.CreatedAt.Equal(b.CreatedAt)
}

// recordedApprovalMirrorIntent restores both true and false for the same
// pending occurrence, together with its canonical creation time for the display
// ID. A non-resumable node-listed dialog may be relisted after startup cleanup
// cancelled its row; only the identical timestamp with no owner answer restores
// that intent. Only a genuinely new card falls back to the current setting.
func (s *Service) recordedApprovalMirrorIntent(ctx context.Context, ix proto.Interaction) (intent bool, at time.Time, found bool) {
	if ix.CreatedAt.IsZero() {
		return false, time.Time{}, false
	}
	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	if p != nil {
		same := sameApprovalOccurrence(ix, p.ix)
		intent, at := p.ix.MirrorMain, p.ix.CreatedAt
		s.fleet.mu.Unlock()
		if same {
			return intent, at, true
		}
		return false, time.Time{}, false
	}
	s.fleet.mu.Unlock()
	st, ok := s.store().(interactionRecorder)
	if !ok {
		return false, time.Time{}, false
	}
	readCtx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	rec, err := st.InteractionRecord(readCtx, ix.ID)
	if err != nil || rec == nil || !sameApprovalOccurrence(ix, rec.Interaction) {
		return false, time.Time{}, false
	}
	startupCancelled := !ix.Resumable && rec.Status == "cancelled" && rec.Resolution == nil
	if rec.Status != "pending" && !startupCancelled {
		return false, time.Time{}, false
	}
	return rec.Interaction.MirrorMain, rec.Interaction.CreatedAt, true
}

// pendingDisplayInteractions is the reconnect snapshot: authoritative pending
// operations plus their display copies, adjacent and in the same order.
func (s *Service) pendingDisplayInteractions(ctx context.Context) []proto.Interaction {
	pending := s.pendingInteractions()
	out := make([]proto.Interaction, 0, len(pending)*2)
	for _, ix := range pending {
		out = append(out, ix)
		if !ix.MirrorMain {
			continue // an existing unmirrored occurrence must not gain a copy here
		}
		if mirror, ok := s.mainApprovalMirror(ctx, ix); ok {
			out = append(out, mirror)
		}
	}
	return out
}
