package db

import (
	"context"
	"testing"
	"time"
)

// Partner hubs and their codes: a code pairs once and only in its time, pairing again revokes the credential issued
// before (a mesh peer's of the same hub_id stays), the dial setting and what a live partner says are kept.
func TestPartnerHubsAndCodes(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	if err := d.CreatePartnerCode(ctx, "h1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.PartnerCodeValid(ctx, "h1"); err != nil || !ok {
		t.Fatalf("fresh code valid = %v, %v", ok, err)
	}
	// A mesh credential of the same hub_id is not the partner's to revoke.
	peerCred := DeviceToken{ID: "dev_mesh", Name: "mesh", Kind: "peer", Role: "peer", MachineID: "hub-b"}
	if err := d.CreateDeviceToken(ctx, peerCred, "meshhash"); err != nil {
		t.Fatal(err)
	}
	p := PartnerHub{HubID: "hub-b", Name: "B", FP: "", Endpoints: nil, Token: "sealed", CredentialID: "dev_b", Dial: "never"}
	cred := DeviceToken{ID: "dev_b", Name: "Partner hub: B", Kind: "partner", Role: "partner", MachineID: "hub-b"}
	if ok, err := d.RedeemPartnerCode(ctx, "h1", p, cred, "credhash1"); err != nil || !ok {
		t.Fatalf("redeem = %v, %v", ok, err)
	}
	cred2 := DeviceToken{ID: "dev_b2", Name: "Partner hub: B", Kind: "partner", Role: "partner", MachineID: "hub-b"}
	if ok, err := d.RedeemPartnerCode(ctx, "h1", p, cred2, "credhash2"); err != nil || ok {
		t.Fatalf("second redeem = %v, %v", ok, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash2"); tok != nil {
		t.Fatal("a used code still made a credential")
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash1"); tok == nil || tok.Role != "partner" || tok.MachineID != "hub-b" {
		t.Fatalf("credential %+v", tok)
	}
	list, err := d.PartnerHubs(ctx)
	if err != nil || len(list) != 1 || list[0].Dial != "never" || list[0].Endpoints == nil || len(list[0].Endpoints) != 0 || list[0].SeenAt != nil {
		t.Fatalf("partners %+v, %v", list, err)
	}
	// Pairing again with a new code revokes the partner credential issued before, not the mesh one.
	if err := d.CreatePartnerCode(ctx, "h2", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	p.Dial, p.CredentialID = "auto", cred2.ID
	if ok, err := d.RedeemPartnerCode(ctx, "h2", p, cred2, "credhash2"); err != nil || !ok {
		t.Fatalf("re-pair = %v, %v", ok, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash1"); tok != nil {
		t.Fatal("the old partner credential still works")
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "meshhash"); tok == nil {
		t.Fatal("pairing a partner revoked a mesh credential")
	}
	// Seen for an older pairing (another credential) changes nothing; for the current one it is kept.
	if err := d.UpdatePartnerSeen(ctx, PartnerHub{HubID: "hub-b", Name: "old", FP: "fp-old", CredentialID: cred.ID}); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.PartnerHubs(ctx); list[0].Name != "B" || list[0].FP != "" || list[0].SeenAt != nil {
		t.Fatalf("seen for an older pairing: %+v", list[0])
	}
	if err := d.UpdatePartnerSeen(ctx, PartnerHub{HubID: "hub-b", Name: "B2", FP: "fp-b", Endpoints: []string{"https://[2001:db8::1]:18662"},
		CredentialID: cred2.ID}); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.SetPartnerDial(ctx, "hub-b", "never"); err != nil || !ok {
		t.Fatalf("set dial = %v, %v", ok, err)
	}
	if ok, _ := d.SetPartnerDial(ctx, "hub-x", "never"); ok {
		t.Fatal("set the dial of a hub that is not paired")
	}
	list, _ = d.PartnerHubs(ctx)
	if got := list[0]; got.Name != "B2" || got.FP != "fp-b" || got.Endpoints[0] != "https://[2001:db8::1]:18662" || got.Dial != "never" ||
		got.SeenAt == nil || got.CredentialID != "dev_b2" {
		t.Fatalf("after seen: %+v", got)
	}
	// SavePartnerHub replaces (the joining side's save).
	p2 := PartnerHub{HubID: "hub-c", Name: "C", FP: "fp-c", Endpoints: []string{"https://hub.example.com:18662"}, Token: "sealed-c", CredentialID: "dev_c"}
	if err := d.SavePartnerHub(ctx, p2); err != nil {
		t.Fatal(err)
	}
	p2.Name = "C2"
	if err := d.SavePartnerHub(ctx, p2); err != nil {
		t.Fatal(err)
	}
	list, _ = d.PartnerHubs(ctx)
	if len(list) != 2 || list[1].Name != "C2" || list[1].Dial != "auto" {
		t.Fatalf("saved: %+v", list)
	}
	// An expired code is not valid, and pruning drops it.
	if err := d.CreatePartnerCode(ctx, "h3", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.PartnerCodeValid(ctx, "h3"); ok {
		t.Fatal("expired code valid")
	}
	if ok, _ := d.RedeemPartnerCode(ctx, "h3", p, DeviceToken{ID: "dev_b3", Kind: "partner", Role: "partner", MachineID: "hub-b"}, "credhash3"); ok {
		t.Fatal("an expired code paired")
	}
	if err := d.PrunePartnerCodes(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM partner_codes WHERE code_hash = 'h3'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("pruned: %d left, %v", left, err)
	}
	if ok, err := d.DeletePartnerHub(ctx, "hub-b"); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if ok, _ := d.DeletePartnerHub(ctx, "hub-b"); ok {
		t.Fatal("deleted twice")
	}
}
