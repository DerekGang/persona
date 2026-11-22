package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// What the manager keeps for a pairing belongs to it alone: a hub unpaired and paired again under the same hub_id
// (PartnerHub.PairedAt later) starts with the owner asked again, and nothing approved or queued for the earlier pairing
// goes to the new one.

// pairAgain has the hub say partner list (as its partner.changed does) and answer partner.hubs with it.
func (r *partnerRig) pairAgain(list assistantwire.PartnerHubs) {
	r.h.pmu.Lock()
	r.h.list = list
	r.h.pmu.Unlock()
	r.s.partnersChanged(list)
}

// prefsRoute is the owner's GET or PUT of the partner prefs routes.
func (r *partnerRig) prefsRoute(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (r *partnerRig) hubCards() []proto.Interaction {
	return slices.DeleteFunc(r.s.pendingQuestions(), func(q proto.Interaction) bool { return q.ToolName != "hub_send" })
}

// The owner's "don't ask" and what waits for a pairing end with it: paired again under its hub_id, the message waiting
// for a card fails (the card taken back) and the queued one too, each told once, nothing goes; hub_list, hub_send and
// the settings route ask again until the owner switches it off for this pairing, and a hub not paired has no setting.
func TestPartnerPairedAgainStartsOver(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	if err := r.db.SetPartnerConfirm(ctx, pHub, false, partnerPairedAt); err != nil {
		t.Fatal(err)
	}
	out, _ := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"queued for the first pairing"}`)
	queued := messageIDOf(t, out)
	if err := r.db.SetPartnerConfirm(ctx, pHub, true, partnerPairedAt); err != nil {
		t.Fatal(err)
	}
	out, _ = r.tool(pSide, "hub_send", `{"hub":"hub-b","text":"waiting for the first pairing's card"}`)
	confirming := messageIDOf(t, out)
	card := r.card(t)
	if err := r.db.SetPartnerConfirm(ctx, pHub, false, partnerPairedAt); err != nil {
		t.Fatal(err)
	}

	// Unpaired and paired again: a later pairing time under the same hub_id.
	again := time.Now().UTC().Truncate(time.Microsecond)
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{{HubID: pHub, Name: "Hub B", Online: true,
		Dial: assistantwire.PartnerDialAuto, PairedAt: again}}})
	for _, id := range []string{queued, confirming} {
		if m := r.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired || m.NextTry != nil {
			t.Fatalf("%s: %+v", id, m)
		}
	}
	waitFor(t, "the card taken back", func() bool { return len(r.hubCards()) == 0 })
	for _, c := range []struct{ conv, text string }{{pMain, "queued for the first pairing"}, {pSide, "waiting for the first pairing's card"}} {
		waitFor(t, "told in "+c.conv, func() bool {
			return slices.ContainsFunc(r.told(c.conv), func(n string) bool {
				return strings.Contains(n, "两个 hub 已经不再配对") && strings.Contains(n, c.text)
			})
		})
	}
	// An allow with "don't ask" that comes late, for the card taken back, changes nothing.
	r.s.partnerAnswered(ctx, r.s.partnerStore(), r.row(t, partnerOut, confirming), proto.Resolution{Allow: boolp(true), Always: true})
	if m := r.row(t, partnerOut, confirming); m.State != partnerFailed || m.CardID != card.ID {
		t.Fatalf("answered after it failed: %+v", m)
	}
	r.loop(t)

	// The setting was the first pairing's: the new one asks, and the route lists none.
	if list, _ := r.tool(pMain, "hub_list", `{}`); !strings.Contains(list, `"Hub B" (hub_id hub-b): online; a message waits for the user's OK`) {
		t.Fatalf("hub_list: %q", list)
	}
	if code, out := r.prefsRoute(t, "GET", "/api/v1/partner-messages/prefs", ""); code != http.StatusOK || len(out["prefs"].([]any)) != 0 {
		t.Fatalf("prefs of the new pairing: %d %v", code, out)
	}
	out, isErr := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"to the new pairing"}`)
	if isErr || !strings.Contains(out, "Waiting for the user's OK") {
		t.Fatalf("hub_send to the new pairing: %q", out)
	}
	if m := r.row(t, partnerOut, messageIDOf(t, out)); m.State != partnerConfirming {
		t.Fatalf("new pairing's row: %+v", m)
	}

	// The owner switches it off for this pairing: kept with it, and messages go without a card.
	if code, out := r.prefsRoute(t, "PUT", "/api/v1/partner-messages/prefs/hub-b", `{"confirm":false}`); code != http.StatusOK || out["confirm"] != false {
		t.Fatalf("put: %d %v", code, out)
	}
	if prefs, _ := r.db.PartnerPrefs(ctx); len(prefs) != 1 || prefs[0].Confirm || !samePairing(prefs[0].PairedAt, again) {
		t.Fatalf("stored: %+v", prefs)
	}
	if code, out := r.prefsRoute(t, "GET", "/api/v1/partner-messages/prefs", ""); code != http.StatusOK ||
		!strings.Contains(string(mustJSON(out)), `{"confirm":false,"hub_id":"hub-b"}`) {
		t.Fatalf("prefs: %d %v", code, out)
	}
	out, _ = r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"without a card now"}`)
	r.waitState(t, partnerOut, messageIDOf(t, out), partnerSent)
	for _, m := range r.h.sentOf(assistantwire.PartnerKindMessage) {
		if m.Envelope.MessageID == queued || m.Envelope.MessageID == confirming {
			t.Fatalf("a message of the first pairing went to the second: %+v", m)
		}
	}

	// A hub that is not paired has no setting to change.
	if code, out := r.prefsRoute(t, "PUT", "/api/v1/partner-messages/prefs/hub-x", `{"confirm":false}`); code != http.StatusNotFound ||
		out["code"] != "partner_not_found" {
		t.Fatalf("put for a hub not paired: %d %v", code, out)
	}
}

// A hub that is no longer listed fails what waits for it as unpaired at once, card and queue alike, without a try.
func TestPartnerUnpairedFailsWhatWaits(t *testing.T) {
	r := newPartnerRig(t)
	out, _ := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"still waiting for the card"}`)
	id := messageIDOf(t, out)
	r.card(t)
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true})
	if m := r.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("row: %+v", m)
	}
	waitFor(t, "the card taken back", func() bool { return len(r.hubCards()) == 0 })
	waitFor(t, "told once", func() bool {
		return len(slices.DeleteFunc(r.told(pMain), func(n string) bool { return !strings.Contains(n, "两个 hub 已经不再配对") })) == 1
	})
	if got := r.h.sentOf(""); len(got) != 0 {
		t.Fatalf("sent: %+v", got)
	}
}

// A message kept before the pairing there is now (a manager that missed the unpairing: a restart) is never sent and
// never asked about again: the next manager's loop fails it as unpaired, told once; a status of then is dropped untold.
func TestPartnerSendsNothingOfAnEarlierPairing(t *testing.T) {
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ctx := context.Background()
	before, due := partnerPairedAt.Add(-time.Hour), time.Now().Add(-time.Second)
	for _, m := range []db.PartnerMessage{
		{ID: "pr_e1", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_e0000000000000000000000000000001",
			Kind: assistantwire.PartnerKindMessage, Hop: 1, MachineID: "server", SessionID: pSide, Text: "queued then", State: partnerQueued,
			NextTry: &due, CreatedAt: before},
		{ID: "pr_e2", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_e0000000000000000000000000000002",
			Kind: assistantwire.PartnerKindMessage, Hop: 1, MachineID: "server", SessionID: pSide, Text: "asked then", State: partnerConfirming,
			CreatedAt: before},
		{ID: "pr_e3", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_e0000000000000000000000000000003",
			Kind: assistantwire.PartnerKindStatus, ReplyTo: "pm_e0000000000000000000000000000009", Hop: 1, Text: assistantwire.PartnerStateDelivered,
			State: partnerQueued, NextTry: &due, CreatedAt: before},
	} {
		if _, _, err := database.InsertPartnerMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	again := newPartnerRigOn(t, database)
	again.loop(t)
	for _, id := range []string{"pm_e0000000000000000000000000000001", "pm_e0000000000000000000000000000002", "pm_e0000000000000000000000000000003"} {
		if m := again.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
			t.Fatalf("%s: %+v", id, m)
		}
	}
	waitFor(t, "both messages told", func() bool {
		n := 0
		for _, text := range again.told(pSide) {
			if strings.Contains(text, "两个 hub 已经不再配对") {
				n++
			}
		}
		return n == 2
	})
	if got := again.h.sentOf(""); len(got) != 0 {
		t.Fatalf("sent: %+v", got)
	}
	if cards := again.hubCards(); len(cards) != 0 {
		t.Fatalf("asked about a message of an earlier pairing: %+v", cards)
	}
}

// A partner names itself, so the owner's card names it with the start of its hub_id too, and hub_list says when two
// partners have one name: one named like another is told apart.
func TestPartnerLookalikeNames(t *testing.T) {
	r := newPartnerRig(t)
	const twin = "7c1e4a2b-9d3f-4c2a-8b1e-0123456789ab"
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{
		{HubID: pHub, Name: "Hub B", Online: true, PairedAt: partnerPairedAt},
		{HubID: twin, Name: "hub b", Online: true, PairedAt: partnerPairedAt},
	}})
	list, _ := r.tool(pMain, "hub_list", `{}`)
	for _, want := range []string{`"Hub B" (hub_id hub-b): online`, `"hub b" (hub_id ` + twin + `): online`} {
		i := strings.Index(list, want)
		if i < 0 || !strings.Contains(strings.SplitN(list[i:], "\n", 2)[0], "another partner hub has this name too") {
			t.Fatalf("hub_list does not tell %q apart: %q", want, list)
		}
	}
	if out, isErr := r.tool(pMain, "hub_send", `{"hub":"Hub B","text":"which one?"}`); !isErr || !strings.Contains(out, "give its hub_id") {
		t.Fatalf("a name two partners have: %q", out)
	}
	out, isErr := r.tool(pMain, "hub_send", `{"hub":"`+twin+`","text":"to the twin"}`)
	if isErr {
		t.Fatalf("hub_send by hub_id: %q", out)
	}
	ix := r.card(t)
	if !strings.Contains(ix.Summary, "「hub b」（7c1e4a2b）") || !strings.Contains(ix.AlwaysLabel, "「hub b」（7c1e4a2b）") {
		t.Fatalf("the card: %q / %q", ix.Summary, ix.AlwaysLabel)
	}
	if got := partnerShortID(twin); got != "7c1e4a2b" {
		t.Fatalf("short id: %q", got)
	}
}
