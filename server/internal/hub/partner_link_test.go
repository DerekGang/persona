package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// A hub without TLS pairs with one that has it: only it dials (the other waits, dial never), the link comes up, and
// after the link is cut (its connection closed at either end; httptest's CloseClientConnections leaves hijacked
// WebSockets alone) it comes back on the same ports. The dialed side never starts a dialer.
func TestPartnerOneSideDialsAndReconnects(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	if row, _ := b.h.partnerRow(hubA); row.Dial != assistantwire.PartnerDialNever {
		t.Fatalf("B dials a hub without TLS: %+v", row)
	}
	noDialer := func() {
		t.Helper()
		b.h.partners.mu.Lock()
		n := len(b.h.partners.dialers)
		b.h.partners.mu.Unlock()
		if n != 0 {
			t.Fatalf("B runs %d dialer(s)", n)
		}
	}
	noDialer()
	full, _ := b.deviceToken(t, roleFull)
	view := func() partnerHubView {
		t.Helper()
		_, body := b.do(t, "GET", "/api/v1/partner-hubs", full, "")
		var list struct {
			Hubs []partnerHubView `json:"hubs"`
		}
		if err := json.Unmarshal(body, &list); err != nil || len(list.Hubs) != 1 {
			t.Fatalf("list %s", body)
		}
		return list.Hubs[0]
	}
	if v := view(); v.State != "connected" || v.DialedBy != "them" || v.Dial != "never" {
		t.Fatalf("B's view: %+v", v)
	}
	// Cut from B's side (its end of the WebSocket): A dials again.
	first := b.h.partnerLinkTo(hubA)
	_ = first.conn.CloseNow()
	eventually(t, 20*time.Second, "the link back after its WebSocket was cut", func() bool {
		l := b.h.partnerLinkTo(hubA)
		return l != nil && l != first && a.h.partnerLinkTo(hubB) != nil
	})
	// Cut from A's side: back again, same ports.
	second := b.h.partnerLinkTo(hubA)
	_ = a.h.partnerLinkTo(hubB).conn.CloseNow()
	eventually(t, 20*time.Second, "the link back after A's end was cut", func() bool {
		l := b.h.partnerLinkTo(hubA)
		return l != nil && l != second && a.h.partnerLinkTo(hubB) != nil
	})
	noDialer()
	// With B set to never and the link gone, B stays offline (not connecting).
	if v := view(); v.State != "connected" || v.LastSeen == nil {
		t.Fatalf("B's view after: %+v", v)
	}
}

// Both hubs dial: one link stays, the one the smaller hub_id dialed; a second link the larger one dials is refused
// (by itself, or closed 4011 by the other), and the kept one stays.
func TestPartnerDuplicateLink(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	eventually(t, 10*time.Second, "the link A dialed", func() bool {
		la, lb := a.h.partnerLinkTo(hubB), b.h.partnerLinkTo(hubA)
		return la != nil && lb != nil && la.dialer == hubA && lb.dialer == hubA
	})
	kept := a.h.partnerLinkTo(hubB)
	row, _ := b.h.partnerRow(hubA)
	token, err := b.h.openKey(row.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, c, err := b.h.dialPartner(ctx, row.FP, token, b.h.partnerEndpoints(hubA))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.h.runPartnerLink(ctx, conn, hubA, hubB, c.key, c.url, ""); err == nil {
		t.Fatal("the second link was served")
	} else if !errors.Is(err, errDuplicateLink) && websocket.CloseStatus(err) != partnerCloseDuplicate {
		t.Logf("the second link ended with %v", err) // A may close it before or after the hellos
	}
	eventually(t, 5*time.Second, "the first link kept", func() bool {
		lb := b.h.partnerLinkTo(hubA)
		return a.h.partnerLinkTo(hubB) == kept && lb != nil && lb.dialer == hubA
	})
}

// Unpairing from either side ends it on both: the pairing forgotten, the link closed, the credentials revoked (the
// one the other held no longer opens the link), no dialer left; revoking the credential from Settings › Devices is
// unpairing too. Pairing again works after.
func TestPartnerUnpair(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	unpaired := func(what string) {
		t.Helper()
		eventually(t, 10*time.Second, what, func() bool {
			_, ra := a.h.partnerRow(hubB)
			_, rb := b.h.partnerRow(hubA)
			a.h.partners.mu.Lock()
			da := len(a.h.partners.dialers)
			a.h.partners.mu.Unlock()
			b.h.partners.mu.Lock()
			db := len(b.h.partners.dialers)
			b.h.partners.mu.Unlock()
			return !ra && !rb && a.h.partnerLinkTo(hubB) == nil && b.h.partnerLinkTo(hubA) == nil && da == 0 && db == 0 &&
				len(a.store.partners) == 0 && len(b.store.partners) == 0
		})
	}
	// From A, over the apps' route.
	pairPartners(t, a, b)
	_, heldByA := partnerCredOf(t, a, b) // B's credential for A
	fullA, _ := a.deviceToken(t, roleFull)
	if code, body := a.do(t, "DELETE", "/api/v1/partner-hubs/"+hubB, fullA, ""); code != 200 {
		t.Fatalf("unpair: %d %s", code, body)
	}
	unpaired("unpaired from A")
	if code, _ := b.do(t, "GET", partnerLinkPath, heldByA, ""); code != http.StatusUnauthorized {
		t.Fatalf("the credential B issued still opens its link: %d", code)
	}
	if code, _ := a.do(t, "DELETE", "/api/v1/partner-hubs/"+hubB, fullA, ""); code != http.StatusNotFound {
		t.Fatalf("unpair twice: %d", code)
	}
	// From B, by revoking the credential it issued A (Settings › Devices).
	pairPartners(t, a, b)
	issued, _ := partnerCredOf(t, b, a)
	fullB, _ := b.deviceToken(t, roleFull)
	if code, body := b.do(t, "DELETE", "/api/v1/auth/tokens/"+issued.ID, fullB, ""); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	unpaired("unpaired by revoking B's credential")
	// From B, with the command line's route.
	pairPartners(t, a, b)
	if _, err := b.h.unpairPartner(context.Background(), hubA, true); err != nil {
		t.Fatal(err)
	}
	unpaired("unpaired from B")
}

// The dial setting: never stops dialing (and the hub waits), auto starts it again; an unknown hub or setting is refused.
func TestPartnerDialSetting(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	fullA, _ := a.deviceToken(t, roleFull)
	fullB, _ := b.deviceToken(t, roleFull)
	for _, mh := range []struct {
		h   *testHub
		tok string
		id  string
	}{{a, fullA, hubB}, {b, fullB, hubA}} {
		if code, body := mh.h.do(t, "PATCH", "/api/v1/partner-hubs/"+mh.id, mh.tok, `{"dial":"never"}`); code != 200 {
			t.Fatalf("dial never: %d %s", code, body)
		}
	}
	if row, _ := a.h.partnerRow(hubB); row.Dial != "never" || a.store.partners[hubB].Dial != "never" {
		t.Fatalf("A's pairing after never: %+v", row)
	}
	_ = a.h.partnerLinkTo(hubB).conn.CloseNow()
	eventually(t, 10*time.Second, "no link, and both offline", func() bool {
		return a.h.partnerLinkTo(hubB) == nil && b.h.partnerLinkTo(hubA) == nil
	})
	_, body := a.do(t, "GET", "/api/v1/partner-hubs", fullA, "")
	var list struct {
		Hubs []partnerHubView `json:"hubs"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Hubs[0].State != "offline" {
		t.Fatalf("A's view: %s", body)
	}
	if code, body := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"auto"}`); code != 200 || !json.Valid(body) {
		t.Fatalf("dial auto: %d %s", code, body)
	}
	waitPartnerLink(t, a, b)
	if code, _ := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"sometimes"}`); code != http.StatusBadRequest {
		t.Fatalf("a bad dial: %d", code)
	}
	if code, _ := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubC, fullA, `{"dial":"never"}`); code != http.StatusNotFound {
		t.Fatalf("an unknown hub's dial: %d", code)
	}
}

// Stopping a hub closes its partner links (an accepted one holds a WebSocket handler, which the shutdown waits for):
// the other side sees the link go.
func TestPartnerLinksStopWithTheHub(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	b.h.stopPartnerLinks()
	eventually(t, 10*time.Second, "A sees the link go", func() bool { return a.h.partnerLinkTo(hubB) == nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { b.h.wsHandlers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("a WebSocket handler stayed after the partner links stopped")
	}
}
