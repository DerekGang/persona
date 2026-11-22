package hub

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The managers' side of partner hubs (assistantwire/partners.go): partner.send hands the default manager's envelope to
// a partner over the link (its msg call), partner.hubs is the list; a partner's msg goes to this hub's default manager
// as partner.receive, stamped with who sent it (the link's hub, the name kept at pairing), never what the envelope
// says. The hub reads no text and keeps no envelope: the managers queue, deduplicate and answer.

var (
	partnerMessageID = lazyre.New(`^pm_[0-9a-f]{32}$`)
	// partnerSendTimeout bounds a send over the link (the partner hub's call to its manager is 20 s of it);
	// partnerReceiveTimeout this hub's call to its manager. Vars: tests shorten them.
	partnerSendTimeout    = 30 * time.Second
	partnerReceiveTimeout = 20 * time.Second
)

// partnerTextCap is the most bytes of text the hubs carry, a transport cap above the managers' PartnerTextMax runes.
const partnerTextCap = 64 << 10

func init() {
	handleBus(assistantwire.MethodPartnerSend, (*Hub).busPartnerSend)
	handleBus(assistantwire.MethodPartnerHubs, func(h *Hub, _ context.Context, _ *busConn, _ json.RawMessage) (any, error) {
		return h.partnerHubsWire(), nil
	})
}

// partnerEnvelopeCode is why an envelope does not read ("" when it does): an id, kind or size the hubs do not carry.
func partnerEnvelopeCode(e assistantwire.PartnerEnvelope) string {
	switch {
	case !partnerMessageID.MatchString(e.MessageID):
		return assistantwire.PartnerCodeInvalid
	case e.Kind != assistantwire.PartnerKindMessage && e.Kind != assistantwire.PartnerKindReply && e.Kind != assistantwire.PartnerKindStatus:
		return assistantwire.PartnerCodeInvalid
	case len(e.Text) > partnerTextCap:
		return assistantwire.PartnerCodeTooLong
	}
	return ""
}

// busPartnerSend is partner.send: only the default manager's, to a partner with a live link.
func (h *Hub) busPartnerSend(ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
	if c.managerID() != h.defaultOwner(ctx) {
		return nil, &rpc.Error{Code: codeOtherManager, Message: assistantwire.MethodPartnerSend + ": only the default manager sends to partner hubs"}
	}
	var p assistantwire.PartnerSend
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodPartnerSend + ": " + err.Error()}
	}
	if code := partnerEnvelopeCode(p.Envelope); code != "" {
		return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodPartnerSend + ": the envelope does not read (" + code + ")"}
	}
	if _, paired := h.partnerRow(p.To); !paired || !h.partnersOn() {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerUnknown, Message: "not a partner hub of this hub"}
	}
	l := h.partnerLinkTo(p.To)
	if l == nil {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerOffline, Message: "no link to that partner hub now"}
	}
	cctx, cancel := context.WithTimeout(ctx, partnerSendTimeout)
	defer cancel()
	var res assistantwire.PartnerResult
	if err := l.rpc.Call(cctx, "msg", p.Envelope, &res); err != nil {
		var re *rpc.Error
		if errors.As(err, &re) && (re.Code == assistantwire.CodePartnerUnavailable || re.Code == assistantwire.CodePartnerUnknown) {
			return nil, &rpc.Error{Code: re.Code, Message: re.Message} // the partner hub's word
		}
		if errors.As(err, &re) {
			return nil, &rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: "the partner hub could not take it: " + re.Message}
		}
		// Dropped, timed out, or never written: in doubt, the manager sends it again (the partner deduplicates).
		return nil, &rpc.Error{Code: assistantwire.CodePartnerOffline, Message: "the link to that partner hub dropped: " + err.Error()}
	}
	return res, nil
}

// partnerInbound is a partner's msg: to this hub's default manager when it takes them, as partner.receive.
func (h *Hub) partnerInbound(ctx context.Context, l *partnerLink, params json.RawMessage) (any, error) {
	var env assistantwire.PartnerEnvelope
	if err := json.Unmarshal(params, &env); err != nil {
		return assistantwire.PartnerResult{Status: assistantwire.PartnerRefused, Code: assistantwire.PartnerCodeInvalid}, nil
	}
	if code := partnerEnvelopeCode(env); code != "" {
		return assistantwire.PartnerResult{Status: assistantwire.PartnerRefused, Code: code}, nil
	}
	row, paired := h.partnerRow(l.hubID)
	if !paired || !h.partnersOn() {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerUnknown, Message: "not a partner hub of this hub"}
	}
	if !h.defaultManagerNames(assistantwire.CapabilityPartnerHubs) {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: "this hub's assistant does not take partner messages now"}
	}
	cctx, cancel := context.WithTimeout(ctx, partnerReceiveTimeout)
	defer cancel()
	var res assistantwire.PartnerResult
	err := h.asst().call(cctx, assistantwire.MethodPartnerReceive, assistantwire.PartnerReceive{FromHub: l.hubID, FromName: row.Name, Envelope: env}, &res)
	if err != nil {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: "this hub's assistant did not take it: " + err.Error()}
	}
	return res, nil
}
