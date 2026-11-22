package assistant

import (
	"context"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// partnerClaimMost is how many due envelopes the loop takes at a time.
const partnerClaimMost = 20

// partnerLoop hands the queued envelopes to the hub for their partners and takes up incoming ones that did not get in,
// until ctx ends (Run starts it). It looks every partnerTick, and at once when woken (a message allowed, a partner
// online). At its first look it reads the partner list and asks again for every message whose card the manager before
// it had up: a restart cancels those cards (db ExpireStaleInteractions).
func (s *Service) partnerLoop(ctx context.Context) {
	started := false
	for {
		more := false
		if st := s.partnerStore(); st != nil {
			if !started {
				started = true
				s.partnerStart(ctx, st)
			}
			more = s.partnerDue(ctx, st)
		}
		wait := partnerTick
		if s.partners.tick > 0 {
			wait = s.partners.tick
		}
		if more {
			wait = 0
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.partners.wakeC():
			t.Stop()
		case <-t.C:
		}
	}
}

// partnerStart reads the partner list (the hub also sends it after the manager's hello) and raises again the card of
// every message still confirming.
func (s *Service) partnerStart(ctx context.Context, st partnerStore) {
	if _, err := s.partnerList(ctx); err != nil && !errors.Is(err, errPartnersOff) && ctx.Err() == nil {
		s.logf("partner hubs: the list: %v", err)
	}
	rows, err := st.PartnerMessagesInState(ctx, partnerOut, partnerConfirming)
	if err != nil {
		s.logf("partner hubs: the messages waiting for a card: %v", err)
		return
	}
	for _, m := range rows {
		go s.partnerAsk(m)
	}
}

// partnerDue acts on the envelopes whose next try is due; true when there may be more.
func (s *Service) partnerDue(ctx context.Context, st partnerStore) bool {
	rows, err := st.ClaimPartnerMessages(ctx, partnerLease, partnerClaimMost)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("partner hubs: claim: %v", err)
		}
		return false
	}
	for _, m := range rows {
		switch {
		case m.Direction == partnerOut && m.State == partnerQueued:
			s.partnerSendOne(ctx, st, m)
		case m.Direction == partnerIn && m.State == partnerReceived:
			s.partnerDeliverIn(ctx, st, m, true)
		default: // moved on meanwhile: nothing to do
			_, _, _ = partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
				if r.NextTry == nil {
					return false
				}
				r.NextTry = nil
				return true
			})
		}
	}
	return len(rows) == partnerClaimMost
}

// partnerEnvelope is queued envelope m as it goes.
func partnerEnvelope(m db.PartnerMessage) assistantwire.PartnerEnvelope {
	e := assistantwire.PartnerEnvelope{MessageID: m.MessageID, Kind: m.Kind, ReplyTo: m.ReplyTo, Hop: m.Hop, SentAt: m.CreatedAt.UTC()}
	if m.Kind == assistantwire.PartnerKindStatus {
		e.State, e.Code = m.Text, m.Code // a status's row keeps the state it reports in its text
	} else {
		e.Text = m.Text
	}
	return e
}

// partnerRetry is how long a message waits after its attempts-th try found the partner away: 5 s, doubling to 5 min.
func (s *Service) partnerRetry(attempts int) time.Duration {
	d := partnerRetryFirst
	if s.partners.retry > 0 {
		d = s.partners.retry
	}
	for i := 1; i < attempts && d < partnerRetryMost; i++ {
		d *= 2
	}
	return min(d, partnerRetryMost)
}

// partnerSendOne hands queued envelope m to the hub for its partner. Taken (accepted, or a duplicate of a try that
// went): sent. Refused, or the hubs no longer paired: failed, and the sending conversation is told (a status is not
// told of). Anything else (no link now, the partner's manager away, an answer that never came): it stays queued and is
// tried again under the same message_id, later each time; one queued past partnerQueueMax expires.
func (s *Service) partnerSendOne(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	now := time.Now().UTC()
	if m.Kind != assistantwire.PartnerKindStatus && now.Sub(m.CreatedAt) > partnerQueueMax {
		if _, ok, _ := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
			if r.State != partnerQueued {
				return false
			}
			r.State, r.NextTry = partnerExpired, nil
			return true
		}); ok {
			s.partnerTell(ctx, m, "stale", "")
		}
		return
	}
	state, code := partnerQueued, ""
	var err error
	if pb, ok := s.hub.(partnerBus); ok {
		cctx, cancel := context.WithTimeout(ctx, partnerSendWait)
		var res assistantwire.PartnerResult
		res, err = pb.PartnerSend(cctx, assistantwire.PartnerSend{To: m.PeerHub, Envelope: partnerEnvelope(m)})
		cancel()
		errCode, _ := relayErrCode(err)
		switch {
		case err == nil && (res.Status == assistantwire.PartnerAccepted || res.Status == assistantwire.PartnerDuplicate):
			state = partnerSent
		case err == nil && res.Status == assistantwire.PartnerRefused:
			state, code = partnerFailed, res.Code
			if code == "" {
				code = assistantwire.PartnerCodeInvalid
			}
		case err != nil && errCode == assistantwire.CodePartnerUnknown:
			state, code = partnerFailed, assistantwire.PartnerCodeUnpaired
		}
	} else {
		err = errPartnersOff
	}
	if state == partnerQueued && (m.Attempts == 0 || (m.Attempts+1)%10 == 0) && ctx.Err() == nil {
		s.logf("partner %s: %s %s not taken yet (try %d): %v", short(m.PeerHub), m.Kind, m.MessageID, m.Attempts+1, err)
	}
	_, changed, merr := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerQueued {
			return false
		}
		r.Attempts++
		r.State, r.Code, r.NextTry = state, code, nil
		if state == partnerQueued {
			next := now.Add(s.partnerRetry(r.Attempts))
			r.NextTry = &next
		}
		return true
	})
	if merr != nil {
		s.logf("partner %s: %s: %v", short(m.PeerHub), m.MessageID, merr)
	}
	if !changed {
		return
	}
	switch {
	case state == partnerSent && m.Kind == assistantwire.PartnerKindReply:
		s.partnerReplied(ctx, st, m)
	case state == partnerFailed && m.Kind != assistantwire.PartnerKindStatus:
		event := "refused"
		if code == assistantwire.PartnerCodeUnpaired {
			event = "unpaired"
		}
		s.partnerTell(ctx, m, event, code)
	}
}

// partnerReplied notes the incoming message reply m answers as answered from here.
func (s *Service) partnerReplied(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	in, err := st.PartnerMessageOf(ctx, partnerIn, m.PeerHub, m.ReplyTo)
	if err != nil || in == nil {
		return
	}
	if _, _, err := partnerMutate(ctx, st, *in, func(r *db.PartnerMessage) bool {
		if r.State != partnerDelivered && r.State != partnerReceived {
			return false
		}
		r.State = partnerReplied
		return true
	}); err != nil {
		s.logf("partner %s: replied: %v", in.MessageID, err)
	}
}

// partnerHurry makes what waits for partner peer due now: it came online.
func (s *Service) partnerHurry(peer string) {
	st := s.partnerStore()
	if st == nil {
		return
	}
	ctx := s.runCtx()
	rows, err := st.PartnerMessagesInState(ctx, partnerOut, partnerQueued)
	if err != nil {
		s.logf("partner %s: online: %v", short(peer), err)
		return
	}
	now := time.Now().UTC()
	for _, m := range rows {
		if m.PeerHub != peer || m.NextTry != nil && !m.NextTry.After(now) {
			continue
		}
		_, _, _ = partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
			if r.State != partnerQueued {
				return false
			}
			r.NextTry = &now
			return true
		})
	}
	s.partnerWakeUp()
}
