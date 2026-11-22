package hub

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
)

func partnerEnvelope(kind, text string) assistantwire.PartnerEnvelope {
	return assistantwire.PartnerEnvelope{MessageID: "pm_" + randomHex(16), Kind: kind, Hop: 1, Text: text, SentAt: time.Now().UTC().Truncate(time.Second)}
}

// An envelope goes from A's manager over the link to B's, stamped with who sent it (the link's hub and the name B kept
// at pairing, never the envelope's word), and B's manager's answer comes back to A's; the list reaches each manager
// after its hello (partner.changed) and on asking (partner.hubs), and the feature is listed only while the default
// manager takes partner messages.
func TestPartnerEnvelopeRoundTrip(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	mb := connectPartnerManager(t, b, assistantwire.AssistantID, caps, func(p assistantwire.PartnerReceive) (any, error) {
		if strings.Contains(p.Envelope.Text, "again") {
			return assistantwire.PartnerResult{Status: assistantwire.PartnerDuplicate}, nil
		}
		return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
	})
	eventually(t, 5*time.Second, "partner.changed after the hello", func() bool {
		l, n := ma.lastChanged()
		return n > 0 && l.Enabled && len(l.Hubs) == 0
	})
	if !slices.Contains(a.h.features(), featurePartnerHubs) {
		t.Fatalf("features without partner_hubs: %v", a.h.features())
	}
	pairPartners(t, a, b)
	eventually(t, 5*time.Second, "partner.changed with B online", func() bool {
		l, _ := ma.lastChanged()
		return len(l.Hubs) == 1 && l.Hubs[0].HubID == hubB && l.Hubs[0].Name == "Office" && l.Hubs[0].Online && l.Hubs[0].Dial == "auto"
	})
	var list assistantwire.PartnerHubs
	if err := ma.call(assistantwire.MethodPartnerHubs, map[string]any{}, &list); err != nil || !list.Enabled || len(list.Hubs) != 1 || !list.Hubs[0].Online {
		t.Fatalf("partner.hubs %+v %v", list, err)
	}

	env := partnerEnvelope(assistantwire.PartnerKindMessage, "can you check the build?")
	var res assistantwire.PartnerResult
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res); err != nil ||
		res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("send: %+v %v", res, err)
	}
	got := mb.received()
	if len(got) != 1 || got[0].FromHub != hubA || got[0].FromName != "家里" || got[0].Envelope.MessageID != env.MessageID ||
		got[0].Envelope.Text != env.Text || got[0].Envelope.Kind != env.Kind || !got[0].Envelope.SentAt.Equal(env.SentAt) {
		t.Fatalf("B's manager got %+v", got)
	}
	// The other way, a reply, and B's manager's own answer (duplicate) passed back as it is.
	reply := partnerEnvelope(assistantwire.PartnerKindReply, "again")
	reply.ReplyTo, reply.Hop = env.MessageID, 2
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: reply}, &res); err != nil ||
		res.Status != assistantwire.PartnerDuplicate {
		t.Fatalf("send again: %+v %v", res, err)
	}
	if err := mb.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubA, Envelope: partnerEnvelope("status", "")}, &res); err != nil ||
		res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("B to A: %+v %v", res, err)
	}
	if got := ma.received(); len(got) != 1 || got[0].FromHub != hubB || got[0].FromName != "Office" {
		t.Fatalf("A's manager got %+v", got)
	}

	// Refusals: an envelope that does not read, a hub that is not a partner, another manager than the default.
	bad := env
	bad.MessageID = "pm_1"
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a bad message id: %v", err)
	}
	bad = partnerEnvelope("gossip", "x")
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a bad kind: %v", err)
	}
	bad = partnerEnvelope("message", strings.Repeat("x", partnerTextCap+1))
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a text over the cap: %v", err)
	}
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubC, Envelope: env}, &res); rpcCode(err) != assistantwire.CodePartnerUnknown {
		t.Fatalf("an unknown hub: %v", err)
	}
	other := connectPartnerManager(t, a, "other", caps, nil)
	if err := other.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res); rpcCode(err) != codeOtherManager {
		t.Fatalf("a manager that is not the default: %v", err)
	}
	if len(mb.received()) != 2 {
		t.Fatalf("a refused envelope reached B: %+v", mb.received())
	}
}

// B's manager is away, or does not take partner messages: A's send fails partner_unavailable (it stays queued at A);
// with no link it fails partner_offline; with partner hubs off on A partner_unknown.
func TestPartnerSendWhenItCannotGo(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	pairPartners(t, a, b)
	env := partnerEnvelope(assistantwire.PartnerKindMessage, "hello")
	var res assistantwire.PartnerResult
	send := func() error {
		return ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res)
	}
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnavailable { // no manager on B
		t.Fatalf("B without a manager: %v", err)
	}
	connectPartnerManager(t, b, assistantwire.AssistantID, nil, nil) // one that does not name the capability
	eventually(t, 5*time.Second, "B's manager connected", func() bool { return b.h.busConnOf(assistantwire.AssistantID) != nil })
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnavailable {
		t.Fatalf("B's manager without the capability: %v", err)
	}
	if slices.Contains(b.h.features(), featurePartnerHubs) {
		t.Fatal("partner_hubs listed with a manager that does not take them")
	}
	// No link: both set to never, the link cut.
	fullA, _ := a.deviceToken(t, roleFull)
	fullB, _ := b.deviceToken(t, roleFull)
	a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"never"}`)
	b.do(t, "PATCH", "/api/v1/partner-hubs/"+hubA, fullB, `{"dial":"never"}`)
	_ = a.h.partnerLinkTo(hubB).conn.CloseNow()
	eventually(t, 10*time.Second, "the link gone", func() bool { return a.h.partnerLinkTo(hubB) == nil })
	if err := send(); rpcCode(err) != assistantwire.CodePartnerOffline {
		t.Fatalf("no link: %v", err)
	}
	if _, err := a.h.unpairPartner(context.Background(), hubB, false); err != nil {
		t.Fatal(err)
	}
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnknown {
		t.Fatalf("unpaired: %v", err)
	}
}
