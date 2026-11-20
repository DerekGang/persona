package hub

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

type projectReviewOwnerStore interface {
	SetProjectReviewAppInputs(context.Context, string, bool) error
	RecordProjectReviewOwnerInput(context.Context, db.ProjectReviewOwnerRelay) error
	SettleProjectReviewOwner(context.Context, db.ProjectReviewOwnerRelay) error
	PendingProjectReviewOwners(context.Context) ([]db.ProjectReviewOwnerRelay, error)
	AckProjectReviewOwner(context.Context, db.ProjectReviewOwnerRelay) error
}

// viaMarked: the app's "via" says it sent the message through Siri, Shortcuts, an App Intent or the share sheet. Such
// a message is never the owner's own words (assistant/start.go messageParams); a mark of any kind but "" counts.
func viaMarked(via any) bool {
	text, isText := via.(string)
	return via != nil && !(isText && text == "")
}

// Only the authenticated device message route knows who supplied the input.
// Do not infer this from transcript roles, body metadata or forward text.
// started is the session a start just returned, for when the machine has not listed it yet.
// indirect: the request carried a "via" mark (viaMarked); it is delivered as usual but never counts as owner input.
func (h *Hub) projectReviewAppSource(r *http.Request, machine, session, id string, started *proto.Session, indirect bool) (db.ProjectReviewOwnerRelay, bool) {
	if _, trusted := r.Context().Value(trustedSubmissionKey{}).(devicewire.Submission); !trusted || indirect || session == "" || id == "" {
		return db.ProjectReviewOwnerRelay{}, false
	}
	unscoped := h.isMainOn(machine, session)
	if m := h.machineByID(machine); m != nil {
		row, ok := m.session(session)
		if !ok && started != nil && started.SessionID == session {
			row, ok = *started, true
		}
		if ok {
			if row.Space != "" || assistantSpace(row.Cwd) != "" || row.Origin == "meeting" {
				return db.ProjectReviewOwnerRelay{}, false
			}
			unscoped = unscoped || row.Assistant || m.isAssistant(row)
		}
	}
	return db.ProjectReviewOwnerRelay{MessageID: id, Manager: h.sessionOwner(machine, session), Machine: machine, Session: session,
		Unscoped: unscoped}, true
}

func (h *Hub) settleProjectReviewOwner(ctx context.Context, r *http.Request, machine, session, id, outcome string, indirect bool) (bool, error) {
	if outcome != sendDelivered && outcome != sendLater {
		return false, nil
	}
	p, eligible := h.projectReviewAppSource(r, machine, session, id, nil, indirect)
	store, ok := h.store.(projectReviewOwnerStore)
	if !ok || !eligible {
		return false, nil
	}
	p.SendOutcome = outcome
	return true, store.SettleProjectReviewOwner(ctx, p)
}

// The timer only retries persisted transport. All sources are native ingress
// or lifecycle observations. It never scans conversations for owner input.
func (h *Hub) projectReviewOwnerLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if err := h.deliverProjectReviewOwners(ctx); err != nil && ctx.Err() == nil {
			h.logf("project review: owner input relay: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Hub) deliverProjectReviewOwners(ctx context.Context) error {
	store, ok := h.store.(projectReviewOwnerStore)
	if !ok {
		return nil
	}
	list, err := store.PendingProjectReviewOwners(ctx)
	if err != nil {
		return err
	}
	failed := make(map[string]bool)
	for _, row := range list {
		if failed[row.Manager] || !h.managerCan(row.Manager, assistantwire.CapabilityProjectReview) {
			continue
		}
		if h.sessionOwner(row.Machine, row.Session) == row.Manager {
			bound, cancel := context.WithTimeout(ctx, time.Second)
			p := assistantwire.ProjectReviewOwnerInput{
				SessionRef: assistantwire.SessionRef{MachineID: row.Machine, SessionID: row.Session},
				MessageID:  row.MessageID, At: row.At, Unscoped: row.Unscoped,
			}
			err := h.asstOf(row.Manager).call(bound, assistantwire.MethodProjectReviewOwnerInput, p, nil)
			cancel()
			if err != nil {
				failed[row.Manager] = true
				continue
			}
		}
		if err := store.AckProjectReviewOwner(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (h *Hub) setProjectReviewAppInputs(ctx context.Context, manager string, enabled bool) error {
	store, ok := h.store.(projectReviewOwnerStore)
	if !ok {
		if enabled {
			return errors.New("project review owner relay requires the hub's Postgres store")
		}
		return nil
	}
	return store.SetProjectReviewAppInputs(ctx, manager, enabled)
}

func (h *Hub) recordAcceptedProjectReviewInput(ctx context.Context, r *http.Request, machine, session, id string, started *proto.Session, indirect bool) {
	p, eligible := h.projectReviewAppSource(r, machine, session, id, started, indirect)
	if store, ok := h.store.(projectReviewOwnerStore); ok && eligible {
		bound, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := store.RecordProjectReviewOwnerInput(bound, p); err != nil {
			h.logf("project review: record accepted app input: %v", err)
		}
	}
}
