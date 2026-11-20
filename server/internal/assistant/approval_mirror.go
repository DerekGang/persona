package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/proto"
)

// approvalMirrorMain controls display copies for new cards. Existing copies keep
// following their authoritative card until that card resolves.
func (s *Service) approvalMirrorMain(ctx context.Context) bool {
	return quietOn(s.settings(ctx).ApprovalMirrorMain)
}

// approvalMirrorID is stable across updates and reconnects without accepting a
// node-chosen display ID in the authoritative interaction's namespace.
func approvalMirrorID(id string) string {
	return proto.ApprovalMirrorID(id)
}

// assistantApprovalMirrorEligible is the boundary that persisted display intent
// may never bypass: even an existing copy belongs only to an eligible Assistant
// permission, never to an Agent, health, private or nested card.
func (s *Service) assistantApprovalMirrorEligible(ctx context.Context, ix proto.Interaction) bool {
	return ix.Kind == proto.IxPermission && !ix.Private && ix.SessionID != "" && ix.MirrorOf == "" &&
		!s.ixInHealth(ctx, ix) && s.ixInAssistant(ctx, ix)
}

// nonMainAssistantPermission is the narrow routing boundary shared by the main
// conversation copy and the bot's direct chat. Agent sessions, private cards,
// health conversations, questions and display copies themselves never opt in.
func (s *Service) nonMainAssistantPermission(ctx context.Context, ix proto.Interaction) bool {
	if !s.assistantApprovalMirrorEligible(ctx, ix) {
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
	mirror.ID = approvalMirrorID(ix.ID)
	mirror.MirrorOf = ix.ID
	mirror.MirrorMain = false
	mirror.MachineID = mainMachine
	mirror.SessionID = v.Main
	mirror.FoldedUnder, mirror.FoldedAs, mirror.FoldedSide = "", "", ""
	mirror.Muted = false
	return mirror, true
}

// sameApprovalOccurrence distinguishes a resumed card from a later operation
// whose runtime happened to reuse its ID. Zero creation times are not an identity.
func sameApprovalOccurrence(a, b proto.Interaction) bool {
	return !a.CreatedAt.IsZero() && a.ID == b.ID && a.MachineID == b.MachineID && a.SessionID == b.SessionID && a.CreatedAt.Equal(b.CreatedAt)
}

// recordedApprovalMirrorIntent restores both true and false for the same
// pending occurrence. Only a genuinely new card falls back to the current setting.
func (s *Service) recordedApprovalMirrorIntent(ctx context.Context, ix proto.Interaction) (intent, found bool) {
	if !ix.Resumable || ix.CreatedAt.IsZero() {
		return false, false
	}
	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	if p != nil {
		same := sameApprovalOccurrence(ix, p.ix)
		intent := p.ix.MirrorMain
		s.fleet.mu.Unlock()
		if same {
			return intent, true
		}
		return false, false
	}
	s.fleet.mu.Unlock()
	st, ok := s.store().(interactionRecorder)
	if !ok {
		return false, false
	}
	readCtx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	rec, err := st.InteractionRecord(readCtx, ix.ID)
	if err != nil || rec == nil || rec.Status != "pending" || !sameApprovalOccurrence(ix, rec.Interaction) {
		return false, false
	}
	return rec.Interaction.MirrorMain, true
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
