package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/proto"
)

const approvalJournalKey = "assistant.approval_clarifications"

var errApprovalConversationGone = errors.New("the originating conversation no longer exists")

// approvalJournalEntry contains delivery state only. In particular it has no
// executable callback, tool arguments, Resolution.Values or approval fields.
type approvalJournalEntry struct {
	ID          string    `json:"id"`
	MachineID   string    `json:"machine_id"`
	SessionID   string    `json:"session_id"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Status      string    `json:"status,omitempty"`
	Result      string    `json:"result,omitempty"`
	// Finalized means this occurrence has a durable terminal interaction record,
	// or a different occurrence under its ID is already terminal and therefore
	// also prevents the original from replaying. Results never deliver without it.
	Finalized bool `json:"finalized,omitempty"`
	Private   bool `json:"private,omitempty"`
	// Continuation marks a detached command result whose retry instruction is
	// valid only while its one-time grant remains unused.
	Continuation bool      `json:"continuation,omitempty"`
	Accepted     bool      `json:"accepted,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
	// live is process-local and never persisted. A delayed startup retry must not
	// mistake an approval created by this process for an interrupted old one.
	live bool
}

func (s *Service) loadApprovalJournalLocked(ctx context.Context) bool {
	b := &s.approvals
	if b.loaded {
		return true
	}
	var list []approvalJournalEntry
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, approvalJournalKey, &list); err != nil {
			s.logf("load approval clarification journal: %v", err)
			return false
		}
	}
	b.list, b.loaded = list, true
	return true
}

func (s *Service) approvalJournalRecord(ctx context.Context, ix proto.Interaction) (approvalJournalEntry, bool, error) {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return approvalJournalEntry{}, false, errors.New("the clarification journal is unavailable")
	}
	for i := len(b.list) - 1; i >= 0; i-- {
		if approvalJournalMatches(b.list[i], ix) {
			return b.list[i], true, nil
		}
	}
	return approvalJournalEntry{}, false, nil
}

func (s *Service) approvalJournalGrantRecord(ctx context.Context, id, fingerprint string) (approvalJournalEntry, bool, error) {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return approvalJournalEntry{}, false, errors.New("the clarification journal is unavailable")
	}
	digest := sha256.Sum256([]byte(fingerprint))
	for i := len(b.list) - 1; i >= 0; i-- {
		a := b.list[i]
		if a.ID == id && (a.Fingerprint == hex.EncodeToString(digest[:]) || a.Fingerprint == "") {
			return a, true, nil
		}
	}
	return approvalJournalEntry{}, false, nil
}

func (s *Service) saveApprovalJournalLocked(ctx context.Context) error {
	if st := s.store(); st != nil {
		list := s.approvals.list
		if list == nil {
			list = []approvalJournalEntry{}
		}
		return st.PutSetting(ctx, approvalJournalKey, list)
	}
	return nil
}

func approvalJournalOccurrence(ix proto.Interaction) (time.Time, string, error) {
	if ix.Detachable {
		fingerprint, err := detachedFingerprintDigest(ix)
		return ix.CreatedAt, fingerprint, err
	}
	return ix.CreatedAt, "", nil
}

func (s *Service) approvalJournalStarted(ctx context.Context, ix proto.Interaction) error {
	createdAt, fingerprint, err := approvalJournalOccurrence(ix)
	if err != nil {
		return err
	}
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return errors.New("the clarification journal is unavailable")
	}
	if slices.ContainsFunc(b.list, func(a approvalJournalEntry) bool { return approvalJournalMatches(a, ix) }) {
		return nil
	}
	b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID,
		CreatedAt: createdAt, Fingerprint: fingerprint, Private: ix.Private, UpdatedAt: time.Now().UTC(), live: true})
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		b.list = slices.DeleteFunc(b.list, func(a approvalJournalEntry) bool {
			return approvalJournalMatches(a, ix) && a.Result == ""
		})
		return fmt.Errorf("keep the clarification result journal: %w", err)
	}
	return nil
}

// approvalJournalApplying is persisted immediately before the retained
// operation executes. A restart in this state has an uncertain outcome, never a
// fabricated cancellation or success.
func (s *Service) approvalJournalApplying(ctx context.Context, ix proto.Interaction) error {
	createdAt, fingerprint, err := approvalJournalOccurrence(ix)
	if err != nil {
		return err
	}
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return errors.New("the clarification journal is unavailable")
	}
	i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return approvalJournalMatches(a, ix) })
	if i < 0 {
		b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID,
			CreatedAt: createdAt, Fingerprint: fingerprint, Private: ix.Private, live: true})
		i = len(b.list) - 1
	} else if ix.Private {
		b.list[i].Private = true
	}
	b.list[i].live = true
	if b.list[i].Result != "" {
		return errors.New("the approval already has a terminal result")
	}
	b.list[i].Status = "applying"
	b.list[i].UpdatedAt = time.Now().UTC()
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		return fmt.Errorf("save approval execution state: %w", err)
	}
	return nil
}

func (s *Service) approvalJournalAborted(ctx context.Context, ix proto.Interaction) {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return
	}
	b.list = slices.DeleteFunc(b.list, func(a approvalJournalEntry) bool {
		return approvalJournalMatches(a, ix) && a.Result == "" && !a.Accepted
	})
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		s.logf("abort approval clarification journal: %v", err)
	}
}

// approvalResultText always names the literal interaction ID and says that the
// original card is no longer waiting, including when execution is uncertain.
func approvalResultText(id, status, text string) string {
	word := map[string]string{
		"resolved": "applied", "approved": "approved", "covered": "covered by a newer Always allow", "denied": "denied", "expired": "expired",
		"replaced": "replaced", "stale": "stale and cancelled", "cancelled": "cancelled", "dismissed": "dismissed",
		"failed": "left with an uncertain execution outcome",
	}[status]
	if word == "" {
		word = "finished"
	}
	return fmt.Sprintf("Approval %s is %s and is no longer pending. %s", id, word, text)
}

func (s *Service) approvalJournalFinished(ctx context.Context, ix proto.Interaction, status, text string, finalized bool) approvalJournalEntry {
	createdAt, fingerprint, err := approvalJournalOccurrence(ix)
	if err != nil {
		finalized = false
		s.logf("record approval occurrence %s: %v", ix.ID, err)
	}
	b := &s.approvals
	b.mu.Lock()
	if !s.loadApprovalJournalLocked(ctx) {
		b.mu.Unlock()
		return approvalJournalEntry{}
	}
	i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return approvalJournalMatches(a, ix) })
	if i < 0 {
		b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID,
			CreatedAt: createdAt, Fingerprint: fingerprint, Private: ix.Private})
		i = len(b.list) - 1
	} else if ix.Private {
		// Upgrade a legacy or recovered entry before its terminal delivery.
		b.list[i].Private = true
	}
	if b.list[i].Result == "" {
		if b.list[i].CreatedAt.IsZero() {
			b.list[i].CreatedAt = createdAt
		}
		if b.list[i].Fingerprint == "" {
			b.list[i].Fingerprint = fingerprint
		}
		b.list[i].Status = status
		b.list[i].Result = approvalResultText(ix.ID, status, text)
		b.list[i].Continuation = ix.Detachable && status == "approved"
		b.list[i].UpdatedAt = time.Now().UTC()
	}
	b.list[i].Finalized = b.list[i].Finalized || finalized
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		s.logf("save approval result %s: %v", ix.ID, err)
	}
	entry := b.list[i]
	deliver := !b.recovering
	b.mu.Unlock()
	if deliver {
		go s.deliverApprovalResults()
	}
	return entry
}

func sameApprovalJournalOccurrence(a, b approvalJournalEntry) bool {
	if a.ID != b.ID || a.MachineID != b.MachineID || a.SessionID != b.SessionID {
		return false
	}
	if a.Fingerprint != "" || b.Fingerprint != "" {
		return a.Fingerprint == b.Fingerprint
	}
	return a.CreatedAt.Equal(b.CreatedAt)
}

func (s *Service) approvalJournalFinalized(ctx context.Context, entry approvalJournalEntry) error {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return errors.New("the clarification journal is unavailable")
	}
	i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return sameApprovalJournalOccurrence(a, entry) })
	if i < 0 || b.list[i].Finalized {
		return nil
	}
	b.list[i].Finalized = true
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		b.list[i].Finalized = false
		return fmt.Errorf("persist finalized approval occurrence: %w", err)
	}
	return nil
}

// deliverApprovalResults hands each result to the exact conversation or its
// durable relay queue. There is deliberately no fallback to today's main chat.
// A single drain prevents concurrent accepts; a result added during a drain
// triggers one more pass. Transient failures wait for the retry timer; a result
// handed to the conversation or durable queue is removed. A result is
// permanently undeliverable only when the deletion ledger names its original
// conversation; an uncertain execution outcome stays durable even then.
func (s *Service) deliverApprovalResults() {
	b := &s.approvals
	b.mu.Lock()
	if b.recovering {
		b.again = true
		b.mu.Unlock()
		return
	}
	if b.delivering {
		b.again = true
		b.mu.Unlock()
		return
	}
	if !s.loadApprovalJournalLocked(context.Background()) {
		b.mu.Unlock()
		return
	}
	b.delivering = true
	for {
		b.again = false
		var due []approvalJournalEntry
		kept := b.list[:0]
		cleaned := false
		for _, a := range b.list {
			if a.Accepted {
				// Versions before result cleanup retained handed-off entries forever.
				cleaned = true
				continue
			}
			kept = append(kept, a)
			if a.Result != "" {
				due = append(due, a)
			}
		}
		b.list = kept
		if cleaned {
			if err := s.saveApprovalJournalLocked(context.Background()); err != nil {
				s.logf("clean handed-off approval results: %v", err)
			}
		}
		b.mu.Unlock()
		failed := false
		for _, a := range due {
			if !a.Finalized {
				// A live finalizer and startup recovery share this boundary. Do not
				// hand off or delete a result until its occurrence is durably terminal.
				if err := s.finishRecoveredApproval(a); err != nil {
					failed = true
					s.logf("finalize approval result %s before delivery: %v", a.ID, err)
					continue
				}
				a.Finalized = true
			}
			err := s.deliverApprovalResult(a)
			gone := errors.Is(err, errApprovalConversationGone)
			if err != nil && !gone {
				failed = true
				s.logf("deliver approval result %s: %v", a.ID, err)
				continue
			}
			if gone {
				s.logf("discard approval result %s: %v", a.ID, err)
			}
			if a.Status != "approved" {
				// Never discard the last recovery record while a failed durable
				// revocation could still resurrect an execution grant on restart.
				if err := s.revokeDetachedGrant(context.Background(), a); err != nil {
					failed = true
					s.logf("retain approval result %s until grant revocation persists: %v", a.ID, err)
					continue
				}
			}
			if gone && a.Status == "failed" {
				// A deleted destination cannot receive the warning, but its durable
				// record is the last evidence that execution may have happened. The
				// revocation above has still run; only its failure schedules a retry.
				s.logf("retain uncertain approval result %s for recovery: %v", a.ID, err)
				continue
			}
			b.mu.Lock()
			before := len(b.list)
			b.list = slices.DeleteFunc(b.list, func(x approvalJournalEntry) bool {
				return x.ID == a.ID && x.CreatedAt.Equal(a.CreatedAt) && x.Fingerprint == a.Fingerprint &&
					x.UpdatedAt.Equal(a.UpdatedAt) && x.Result == a.Result
			})
			if len(b.list) != before {
				if err := s.saveApprovalJournalLocked(context.Background()); err != nil {
					s.logf("remove handed-off approval result %s: %v", a.ID, err)
				}
			}
			b.mu.Unlock()
		}
		b.mu.Lock()
		if failed {
			s.scheduleApprovalResultRetryLocked()
		}
		if !b.again {
			b.delivering = false
			b.mu.Unlock()
			return
		}
	}
}

func (s *Service) scheduleApprovalResultRetryLocked() {
	b := &s.approvals
	if b.retry {
		return
	}
	b.retry = true
	time.AfterFunc(time.Minute, func() {
		b.mu.Lock()
		b.retry = false
		b.mu.Unlock()
		s.deliverApprovalResults()
	})
}

func (s *Service) deliverApprovalResult(a approvalJournalEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if a.Continuation {
		// Serialize with grant consumption through the actual conversation/queue
		// handoff. If another call consumed first, the late result must not tell
		// the model to execute that same call again (especially under Always).
		s.detached.mu.Lock()
		defer s.detached.mu.Unlock()
		pending, err := s.detachedContinuationPendingLocked(ctx, a.ID)
		if err != nil {
			return fmt.Errorf("check detached continuation before delivery: %w", err)
		}
		if !pending {
			a.Result = consumedContinuationText(a.ID)
		}
	}
	if _, deleted, err := s.deletedConversation(ctx, a.MachineID, a.SessionID); err != nil {
		return fmt.Errorf("check the originating conversation's deletion record: %w", err)
	} else if deleted {
		return errApprovalConversationGone
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := s.call(ctx, a.MachineID, DeviceSessionGet, map[string]any{"session_id": a.SessionID}, &res); err != nil {
		return err
	}
	if !res.OK {
		// session.get reads the manager's current cache. A restart, an offline
		// node, or a list that has not arrived yet says nothing about deletion.
		return errors.New("the originating conversation is not currently listed")
	}
	msg := relayMsg{From: approvalResultFrom + a.ID, FromTitle: "Approval result", Tool: askAnswerTool,
		Text: a.Result, At: a.UpdatedAt, Private: a.Private, Continuation: a.Continuation}
	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	working := false
	if c, ok := s.session(ctx, a.MachineID, a.SessionID); ok {
		working = c.Status == proto.StatusWorking || c.Starting
	}
	if !working {
		if err := s.tryDirectRelay(ctx, a.MachineID, a.SessionID, &msg); err == nil {
			return nil
		} else if s.refusedDirect(ctx, a.MachineID, a.SessionID, msg, err) {
			return nil // a runtime conversation that cannot take it: the owner was pushed (relayRefused)
		} else if !relayInDoubt(err) && !refusedForGood(err) {
			// A transient send refusal may still be retried through the durable
			// queue; an actually missing conversation was rejected above.
			s.logf("approval result %s direct send: %v; queued", a.ID, err)
		}
	}
	return s.queueApprovalResult(a.MachineID, a.SessionID, msg)
}

func consumedContinuationText(id string) string {
	return fmt.Sprintf("Approval %s is no longer pending. Its one-time continuation was already consumed or is no longer available. Do not retry it automatically; check whether the exact call already ran.", id)
}

// tryQueuedApprovalRelay rechecks a detached continuation at the last possible
// boundary. Grant consumption stays excluded through the actual send, including
// when an Always rule would let the requested retry execute without a prompt.
func (s *Service) tryQueuedApprovalRelay(ctx context.Context, machine, to string, original relayMsg) (relayMsg, time.Duration, error) {
	if !original.Continuation {
		return s.tryQueuedRelay(ctx, machine, to, &original)
	}
	msg := original
	s.detached.mu.Lock()
	defer s.detached.mu.Unlock()
	id := strings.TrimPrefix(msg.From, approvalResultFrom)
	pending, err := s.detachedContinuationPendingLocked(ctx, id)
	if err != nil {
		return msg, time.Minute, fmt.Errorf("recheck queued detached continuation: %w", err)
	}
	if !pending {
		msg.Text = consumedContinuationText(id)
	}
	return s.tryQueuedRelay(ctx, machine, to, &msg)
}

// queueApprovalResult deduplicates by the stable once_id before persisting.
func (s *Service) queueApprovalResult(machine, to string, msg relayMsg) error {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(context.Background()) {
		b.mu.Unlock()
		return errors.New("the relay queue is unavailable")
	}
	id := relayMsgID(to, msg)
	for _, old := range b.st.Queue[to] {
		if relayMsgID(to, old) == id {
			b.mu.Unlock()
			return nil
		}
	}
	msg.QueuedAt, msg.Depth = time.Now().UTC(), len(b.st.Queue[to])+1
	b.st.Queue[to] = append(b.st.Queue[to], msg)
	if err := s.saveRelayLocked(); err != nil {
		// The journal still owns this result. Do not acknowledge an in-memory-only
		// queue entry that a restart could lose; let the durable journal retry it.
		q := b.st.Queue[to]
		if len(q) == 1 {
			delete(b.st.Queue, to)
		} else {
			b.st.Queue[to] = q[:len(q)-1]
		}
		b.mu.Unlock()
		return fmt.Errorf("persist approval result relay: %w", err)
	}
	b.mu.Unlock()
	if c, ok := s.session(context.Background(), machine, to); ok {
		s.drainRelays(machine, []proto.Session{c})
	}
	return nil
}

// recoverApprovalJournals is fail-closed: no process-local operation survives a
// manager restart. An interrupted apply is explicitly uncertain; any other
// unfinished clarified card is cancelled without executing.
func (s *Service) recoverApprovalJournals(ctx context.Context) {
	b := &s.approvals
	b.mu.Lock()
	if !s.loadApprovalJournalLocked(ctx) {
		// A transient startup read must not strand delivery forever, including
		// after a later replay observes recovering=true. Keep the gate closed and
		// retry the complete load instead of guessing which journals exist.
		b.recovering, b.recoveryDone = true, false
		if !b.recoveryRetry {
			b.recoveryRetry = true
			time.AfterFunc(time.Minute, func() {
				b.mu.Lock()
				b.recoveryRetry = false
				b.mu.Unlock()
				s.recoverApprovalJournals(context.Background())
			})
		}
		b.mu.Unlock()
		return
	}
	b.recoverySeq++
	seq := b.recoverySeq
	b.recovering, b.recoveryDone = true, false
	var recovered []approvalJournalEntry
	changed := false
	for i := range b.list {
		if b.list[i].Result == "" && b.list[i].live {
			// The first load failed earlier, but this entry was created by the
			// current process after storage recovered. It is not restart residue.
			continue
		}
		if b.list[i].Result == "" {
			if b.list[i].Status == "applying" {
				b.list[i].Status = "failed"
				b.list[i].Result = approvalResultText(b.list[i].ID, "failed", "The assistant restarted while the approved operation was executing. Its execution outcome is uncertain: check current state before retrying; do not assume it applied or that nothing changed.")
			} else {
				b.list[i].Status = "cancelled"
				b.list[i].Result = approvalResultText(b.list[i].ID, "cancelled", "The assistant restarted, so the retained operation was cancelled without executing. Ask for a fresh approval if the change is still wanted.")
			}
			if !b.list[i].CreatedAt.IsZero() || b.list[i].Fingerprint != "" {
				b.list[i].UpdatedAt = time.Now().UTC()
			}
			// A legacy entry has no occurrence fields. Its original UpdatedAt is
			// also its occurrence cutoff; leave it unchanged so this recovery result
			// cannot make a later same-ID card look like the old occurrence.
			changed = true
		}
		// A terminal journal result can be the only durable outcome after a crash
		// between its write and the interaction's terminal write. Close every
		// journaled occurrence before any result delivery can remove its journal.
		recovered = append(recovered, b.list[i])
	}
	if changed {
		if err := s.saveApprovalJournalLocked(ctx); err != nil {
			s.logf("save restarted approval outcomes: %v", err)
		}
	}
	b.mu.Unlock()
	s.finalizeRecoveredApprovals(ctx, seq, recovered)
}

// finalizeRecoveredApprovals closes the whole recovered batch before releasing
// result delivery. A failed terminal interaction write keeps recovery active and
// retries the same batch; no journal can be delivered and forgotten while its
// restored interaction is still pending. The sequence prevents an older retry
// from completing over a newer recovery pass.
func (s *Service) finalizeRecoveredApprovals(ctx context.Context, seq uint64, recovered []approvalJournalEntry) {
	b := &s.approvals
	b.mu.Lock()
	if b.recoverySeq != seq {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	failed := false
	for _, a := range recovered {
		if a.Status != "approved" {
			// A continuation grant is valid only with its completed approved result.
			// Do not hold approvals.mu while taking detached.mu: consumption takes
			// those locks in the opposite order before permitting execution.
			if err := s.revokeDetachedGrant(ctx, a); err != nil {
				failed = true
				s.logf("revoke interrupted approval %s: %v", a.ID, err)
			}
		}
		if err := s.finishRecoveredApproval(a); err != nil {
			failed = true
			s.logf("finish recovered approval %s: %v", a.ID, err)
		}
	}
	b.mu.Lock()
	if b.recoverySeq != seq {
		b.mu.Unlock()
		return
	}
	if failed {
		if !b.recoveryRetry {
			b.recoveryRetry = true
			time.AfterFunc(time.Minute, func() {
				b.mu.Lock()
				b.recoveryRetry = false
				b.mu.Unlock()
				s.finalizeRecoveredApprovals(context.Background(), seq, recovered)
			})
		}
		b.mu.Unlock()
		return
	}
	b.recovering, b.recoveryDone = false, true
	b.mu.Unlock()
	s.deliverApprovalResults()
}

// recoveredApprovalOutcome closes a restored card consistently with its durable
// journal. An unfinished apply is uncertain, another unfinished entry is
// cancelled, and an already recorded outcome is retained without executing it.
func recoveredApprovalOutcome(a approvalJournalEntry) (historyStatus, journalStatus, text string) {
	journalStatus, text = a.Status, a.Result
	if text == "" {
		if journalStatus == "applying" {
			journalStatus = "failed"
			text = "The assistant restarted while the approved operation was executing. Its execution outcome is uncertain: check current state before retrying; do not assume it applied or that nothing changed."
		} else {
			journalStatus = "cancelled"
			text = "The assistant restarted, so the retained operation was cancelled without executing. Ask for a fresh approval if the change is still wanted."
		}
	}
	switch journalStatus {
	case "approved", "resolved", "denied", "covered":
		// resolvePreparedAnswer uses endStatus for history: an owner Allow=false
		// and an Always-covered Allow are both "resolved" there.
		historyStatus = "resolved"
	case "failed":
		// Preserve the live uncertain-execution status instead of conflicting with
		// it by manufacturing "cancelled" during recovery.
		historyStatus = "failed"
	case "dismissed":
		historyStatus = "dismissed"
	case "expired":
		historyStatus = "expired"
	default:
		historyStatus = "cancelled"
	}
	return historyStatus, journalStatus, text
}

func approvalJournalInteraction(entry approvalJournalEntry) proto.Interaction {
	return proto.Interaction{ID: entry.ID, MachineID: entry.MachineID, SessionID: entry.SessionID,
		Kind: proto.IxPermission, CreatedAt: entry.CreatedAt, Detachable: entry.Fingerprint != ""}
}

func approvalJournalMatches(entry approvalJournalEntry, ix proto.Interaction) bool {
	if entry.ID != ix.ID || entry.MachineID != ix.MachineID || entry.SessionID != ix.SessionID {
		return false
	}
	if entry.Fingerprint != "" {
		fingerprint, err := detachedFingerprintDigest(ix)
		return err == nil && fingerprint == entry.Fingerprint
	}
	if entry.CreatedAt.IsZero() {
		// A legacy journal has no explicit occurrence. Never match a card created
		// after that journal's last update; older unknown occurrences fail closed.
		return !ix.CreatedAt.After(entry.UpdatedAt)
	}
	return entry.CreatedAt.Equal(ix.CreatedAt)
}

// finishRecoveredApproval closes only the journal's occurrence. A different
// live occurrence is never claimed. With no matching live card, the fleet mutex
// stays held through the strict database write so restoreInteractions cannot
// validate a pending snapshot and then reinstall it after recovery closes it.
func (s *Service) finishRecoveredApproval(entry approvalJournalEntry) error {
	id := entry.ID
	historyStatus, journalStatus, text := recoveredApprovalOutcome(entry)
	r := proto.Resolution{Dismiss: true, Reason: "assistant_restarted"}
	if journalStatus == "denied" {
		allow := false
		r = proto.Resolution{Allow: &allow, Reason: "assistant_restarted"}
	}
	s.fleet.mu.Lock()
	p := s.fleet.interactions[id]
	if p != nil && !approvalJournalMatches(entry, p.ix) {
		p = nil
	}
	if p == nil {
		candidate := approvalJournalInteraction(entry)
		if recorder, ok := s.store().(interactionRecorder); ok {
			readCtx, cancel := context.WithTimeout(context.Background(), historyTimeout)
			rec, readErr := recorder.InteractionRecord(readCtx, entry.ID)
			cancel()
			if readErr != nil {
				s.fleet.mu.Unlock()
				return readErr
			}
			if rec != nil && approvalJournalMatches(entry, rec.Interaction) {
				// Use the complete stored call when it is the same occurrence. A
				// tombstone deliberately has no tool arguments and must not be
				// mistaken for a different fingerprint.
				candidate = rec.Interaction
			}
		}
		err := s.recordEndOccurrenceStrict(candidate, historyStatus, &r)
		s.fleet.mu.Unlock()
		if err != nil {
			return err
		}
		return s.approvalJournalFinalized(context.Background(), entry)
	}
	s.fleet.mu.Unlock()
	s.fleet.mu.Lock()
	if s.fleet.interactions[id] == p && !p.claimed {
		if p.prepared != nil {
			s.fleet.mu.Unlock()
			s.endPrepared(p, historyStatus, text)
		} else {
			a := &preparedApproval{terminal: true, clarified: true, result: r,
				outcome: approvalops.Outcome{Text: text}, reportStatus: journalStatus}
			p.prepared, p.claimed, p.resolution = a, true, &r
			p.expired = historyStatus == "expired"
			if p.resolved == nil {
				p.resolved = make(chan struct{})
			}
			s.fleet.mu.Unlock()
			_ = s.finishPrepared(p, historyStatus)
		}
	} else {
		s.fleet.mu.Unlock()
	}
	if err := s.recordEndOccurrenceStrict(p.ix, historyStatus, &r); err != nil {
		return err
	}
	return s.approvalJournalFinalized(context.Background(), entry)
}
