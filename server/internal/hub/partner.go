package hub

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// Partner hubs (docs/server/partner-hubs.md): two independent hubs, paired by their owners, whose assistants send each
// other messages. This hub carries the managers' envelopes over one pinned link per partner and keeps nothing of them:
// no group, no records, no App requests, no node streams, no message state. It is not the mesh in any way: partners
// never become peers, never appear in the hello's mesh.hubs, and none of the mesh's state or calls is used.
//
// partner.go is the state and its lifecycle; partner_pair.go pairing and unpairing, partner_link.go the link,
// partner_dial.go dialing, partner_proxy.go the outbound proxy, partner_bus.go the managers' methods,
// partner_routes.go the apps' routes, partner_cli.go the command line.

const (
	rolePartner     = "partner"      // a partner hub's link credential: opens GET /api/v1/partner/link alone
	rolePartnerPair = "partner_pair" // a partner pairing code (jh_…): POST /api/v1/partner/pair alone
	partnerCodeLife = 10 * time.Minute

	partnerLinkPath = "/api/v1/partner/link"
	partnerPairPath = "/api/v1/partner/pair"
)

// partnerState is the partner hubs on h.partners: the pairings (partner_hubs), their links and dialers.
type partnerState struct {
	mu      sync.Mutex
	running bool // initPartners ran and the hub is not stopping
	ctx     context.Context
	rows    map[string]db.PartnerHub   // hub id -> its pairing
	links   map[string]*partnerLink    // hub id -> its live link
	dialers map[string]*peerDialer     // hub id -> the loop dialing it
	dial    map[string]partnerDialFail // hub id -> how dialing it last went
	heard   map[string][]string        // hub id -> the https addresses its last hello named
	// changed is closed and replaced on every change (waiters: the dialers).
	changed chan struct{}
	// kick wakes tellLoop, which tells the default manager (manager) and the apps (apps) the list as it is then.
	kick          chan struct{}
	manager, apps bool
	ping          [2]time.Duration // tests: heartbeat interval and timeout
}

// partnerDialFail is how the last dial of a partner went: nil err after a success.
type partnerDialFail struct {
	err     error
	at      time.Time
	refused bool // the partner does not take this hub's credential (it unpaired, most likely)
}

// partnersOn: partner hubs are on here: hub.json partners.enabled is not false, the hub has where to keep them, and it
// is not a member of a mesh group (whose assistant stands by for the center's).
func (h *Hub) partnersOn() bool {
	return h.cfg != nil && h.cfg.Partners.enabled() && h.partnerStore() != nil && !h.isMember()
}

// initPartners loads the pairings, next to initMesh.
func (h *Hub) initPartners(ctx context.Context) {
	ps := &h.partners
	ps.mu.Lock()
	ps.running, ps.ctx = true, ctx
	ps.rows, ps.links, ps.dialers = map[string]db.PartnerHub{}, map[string]*partnerLink{}, map[string]*peerDialer{}
	ps.dial, ps.heard = map[string]partnerDialFail{}, map[string][]string{}
	ps.changed, ps.kick = make(chan struct{}), make(chan struct{}, 1)
	ps.mu.Unlock()
	if st := h.partnerStore(); st != nil {
		list, err := st.PartnerHubs(ctx)
		if err != nil {
			h.logf("partners: load: %v", err)
		}
		ps.mu.Lock()
		for _, p := range list {
			ps.rows[p.HubID] = p
		}
		ps.mu.Unlock()
	}
	go h.tellLoop(ctx)
}

// startPartners starts dialing the partners this hub dials, next to startMesh: it needs the TLS identity.
func (h *Hub) startPartners(context.Context) {
	ps := &h.partners
	ps.mu.Lock()
	ids := make([]string, 0, len(ps.rows))
	for id := range ps.rows {
		ids = append(ids, id)
	}
	ps.mu.Unlock()
	if !h.partnersOn() {
		if len(ids) > 0 {
			h.logf("partners: off here (partners.enabled, or a mesh member): %d pairing(s) kept, not linked", len(ids))
		}
		return
	}
	h.sayPartnerProxy()
	for _, id := range ids {
		h.startPartnerDialer(id)
	}
}

// stopPartnerLinks is part of the shutdown, next to stopPeerLinks: every link gets a 1001 (an accepted one holds
// enterWS), the dialers stop.
func (h *Hub) stopPartnerLinks() {
	ps := &h.partners
	ps.mu.Lock()
	links := make([]*partnerLink, 0, len(ps.links))
	for _, l := range ps.links {
		links = append(links, l)
	}
	dialers := ps.dialers
	ps.dialers = map[string]*peerDialer{}
	ps.running = false
	ps.mu.Unlock()
	for _, d := range dialers {
		d.cancel()
	}
	for _, l := range links {
		l.close(websocket.StatusGoingAway, "hub stopping")
	}
}

// partnerRow is the pairing with a hub, if there is one.
func (h *Hub) partnerRow(hubID string) (db.PartnerHub, bool) {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.rows[hubID]
	return p, ok
}

// partnerForCredential is the partner a credential was issued to ("" for none).
func (h *Hub) partnerForCredential(id string) string {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for hid, p := range ps.rows {
		if p.CredentialID == id {
			return hid
		}
	}
	return ""
}

// partnerWait is closed at the next change.
func (h *Hub) partnerWait() <-chan struct{} {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.changed == nil {
		return nil
	}
	return ps.changed
}

// partnersChanged: the list, a link, a dial setting or a pairing changed. The dialers look again; the default manager
// hears partner.changed and the apps {"type":"partner_hubs"}.
func (h *Hub) partnersChanged() {
	ps := &h.partners
	ps.mu.Lock()
	if ps.changed != nil {
		close(ps.changed)
		ps.changed = make(chan struct{})
	}
	ps.manager, ps.apps = true, true
	kick := ps.kick
	ps.mu.Unlock()
	if kick != nil {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// partnersToManager has the list told to the default manager alone (it just said its hello).
func (h *Hub) partnersToManager() {
	ps := &h.partners
	ps.mu.Lock()
	ps.manager = true
	kick := ps.kick
	ps.mu.Unlock()
	if kick != nil {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// tellLoop sends what partnersChanged asked for, one at a time and as the list is when it goes (a burst of changes
// goes out as one, never an older list after a newer one).
func (h *Hub) tellLoop(ctx context.Context) {
	ps := &h.partners
	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.kick:
		}
		ps.mu.Lock()
		manager, apps := ps.manager, ps.apps
		ps.manager, ps.apps = false, false
		ps.mu.Unlock()
		if apps {
			h.broadcast(map[string]any{"type": "partner_hubs"})
		}
		if manager && h.defaultManagerNames(assistantwire.CapabilityPartnerHubs) {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			to := h.defaultOwner(cctx)
			cancel()
			if err := h.notifyBus(to, assistantwire.EventPartnerHubs, h.partnerHubsWire()); err != nil {
				h.logf("partners: tell %s: %v", to, err)
			}
		}
	}
}

// partnerHubsWire is the list as the managers get it (partner.hubs, partner.changed): none while partner hubs are off.
func (h *Hub) partnerHubsWire() assistantwire.PartnerHubs {
	out := assistantwire.PartnerHubs{Enabled: h.partnersOn(), Hubs: []assistantwire.PartnerHub{}}
	if !out.Enabled {
		return out
	}
	ps := &h.partners
	ps.mu.Lock()
	for id, p := range ps.rows {
		l := ps.links[id]
		out.Hubs = append(out.Hubs, assistantwire.PartnerHub{HubID: id, Name: p.Name, Online: l != nil && l.ctx.Err() == nil,
			Dial: partnerDialOf(p)})
	}
	ps.mu.Unlock()
	slices.SortFunc(out.Hubs, func(a, b assistantwire.PartnerHub) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.HubID, b.HubID))
	})
	return out
}

func partnerDialOf(p db.PartnerHub) string {
	if p.Dial == assistantwire.PartnerDialNever {
		return assistantwire.PartnerDialNever
	}
	return assistantwire.PartnerDialAuto
}

// partnerFeatures is partner_hubs while partner hubs are on and the default manager takes their messages.
func (h *Hub) partnerFeatures() []string {
	if !h.partnersOn() || !h.defaultManagerNames(assistantwire.CapabilityPartnerHubs) {
		return nil
	}
	return []string{featurePartnerHubs}
}

const featurePartnerHubs = assistantwire.CapabilityPartnerHubs
