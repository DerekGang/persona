package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// partnerCredOf is the credential hub issued to the partner other (its pairing's), and the one other issued it (in
// the clear).
func partnerCredOf(t *testing.T, hub, other *testHub) (issued db.DeviceToken, held string) {
	t.Helper()
	row, ok := hub.h.partnerRow(other.id())
	if !ok {
		t.Fatalf("%s has no pairing with %s", hub.h.cfg.Name, other.h.cfg.Name)
	}
	toks, _ := hub.store.DeviceTokens(context.Background())
	for _, tk := range toks {
		if tk.ID == row.CredentialID {
			issued = tk
		}
	}
	held, err := hub.h.openKey(row.Token)
	if err != nil {
		t.Fatal(err)
	}
	return issued, held
}

// Two hubs pair as partners and link: the pairing is kept on both sides with a credential of kind and role partner
// that opens the partner link alone, the link is the one the smaller hub_id dialed, and the mesh never hears of it
// (no peer, nothing in the hello's mesh.hubs).
func TestPartnerPairAndLink(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	appTok, _ := a.deviceToken(t, roleFull)
	app := dialApp(t, a, appTok)
	pairPartners(t, a, b)
	app.wait(t, "partner_hubs", 0, typeIs("partner_hubs")) // the apps reload the list
	eventually(t, 10*time.Second, "the link the smaller hub_id dialed", func() bool {
		la, lb := a.h.partnerLinkTo(hubB), b.h.partnerLinkTo(hubA)
		return la != nil && lb != nil && la.dialer == hubA && lb.dialer == hubA
	})
	for _, mh := range []*testHub{a, b} {
		mh.h.mesh.mu.Lock()
		peers, links := len(mh.h.mesh.peers), len(mh.h.mesh.links)
		mh.h.mesh.mu.Unlock()
		hubs := mh.h.meshHubs() // this hub alone
		if peers != 0 || links != 0 || len(hubs) != 1 || !hubs[0].Self || len(mh.store.peers) != 0 {
			t.Fatalf("%s: the mesh heard of a partner: %d peers, %d links, hubs %v", mh.h.cfg.Name, peers, links, mh.h.meshHubs())
		}
	}
	ra, _ := a.h.partnerRow(hubB)
	rb, _ := b.h.partnerRow(hubA)
	if ra.Dial != assistantwire.PartnerDialAuto || rb.Dial != assistantwire.PartnerDialAuto || ra.Name != "Office" || rb.Name != "家里" ||
		ra.FP != b.h.public.fp || rb.FP != a.h.public.fp {
		t.Fatalf("pairings: %+v / %+v", ra, rb)
	}
	issued, held := partnerCredOf(t, b, a) // held: the one A issued B
	if issued.Kind != rolePartner || issued.Role != rolePartner || issued.MachineID != hubA || !strings.Contains(issued.Name, "家里") {
		t.Fatalf("credential B issued: %+v", issued)
	}
	// The credential opens the partner link (on A, which issued it) and nothing else.
	if code, body := a.do(t, "GET", "/api/v1/machines", held, ""); code != http.StatusForbidden {
		t.Fatalf("a partner credential on the API: %d %s", code, body)
	}
	if code, _ := a.do(t, "POST", partnerPairPath, held, "{}"); code != http.StatusForbidden {
		t.Fatalf("a partner credential pairing: %d", code)
	}
	// The apps' list (full credentials), on both sides.
	full, _ := a.deviceToken(t, roleFull)
	code, body := a.do(t, "GET", "/api/v1/partner-hubs", full, "")
	var list struct {
		Enabled  bool             `json:"enabled"`
		Member   bool             `json:"member"`
		CanIssue bool             `json:"can_issue"`
		Hubs     []partnerHubView `json:"hubs"`
	}
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || !list.Enabled || list.Member || !list.CanIssue || len(list.Hubs) != 1 {
		t.Fatalf("list: %d %s", code, body)
	}
	if v := list.Hubs[0]; v.HubID != hubB || v.State != "connected" || v.DialedBy != "us" || v.Since == nil || v.Dial != "auto" || v.FP == "" {
		t.Fatalf("view: %s", body)
	}
	answer, _ := a.deviceToken(t, roleAnswer)
	if code, _ := a.do(t, "GET", "/api/v1/partner-hubs", answer, ""); code != http.StatusForbidden {
		t.Fatalf("an answer-only device read the partner hubs: %d", code)
	}
}

// A code works once, only within its time, never on its own hub; codes that do not read are refused before anything
// is sent, and a failed pairing leaves no live credential behind.
func TestPartnerPairingCode(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{})
	ctx := context.Background()
	code, err := b.h.newPartnerCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code.Code, "persona://partner?") || !strings.Contains(code.Code, "code=jh_") ||
		time.Until(code.ExpiresAt) > partnerCodeLife || time.Until(code.ExpiresAt) < partnerCodeLife-time.Minute {
		t.Fatalf("code %+v", code)
	}
	if _, err := b.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "own_code" {
		t.Fatalf("own code: %v", err)
	}
	if _, err := a.h.joinPartner(ctx, code.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := c.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "code_refused" || partnerErrStatus(err) != http.StatusUnauthorized {
		t.Fatalf("a used code: %v", err)
	}
	expired, _ := b.h.newPartnerCode(ctx)
	b.store.mu.Lock()
	for k := range b.store.partnerCodes {
		if !b.store.usedPartnerCodes[k] {
			b.store.partnerCodes[k] = time.Now().Add(-time.Second)
		}
	}
	b.store.mu.Unlock()
	if _, err := c.h.joinPartner(ctx, expired.Code); partnerErrCode(err) != "code_refused" {
		t.Fatalf("an expired code: %v", err)
	}
	toks, _ := c.store.DeviceTokens(ctx)
	for _, tk := range toks {
		if tk.Kind == rolePartner && !c.store.revoked[tk.ID] {
			t.Fatalf("a failed pairing left a live credential: %+v", tk)
		}
	}
	if _, ok := c.h.partnerRow(hubB); ok {
		t.Fatal("a failed pairing was kept")
	}
	for raw, want := range map[string]string{
		"https://hub.example.com":                                                                            "bad_code",
		"persona://peer?hub=x&code=jx_" + randomHex(32):                                                      "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://192.0.2.1&code=jh_short":                        "bad_code",
		"persona://partner?hub=" + hubB + "&url=https://192.0.2.1&code=jh_" + randomHex(32):                  "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=http://192.0.2.1:18660&code=jh_" + randomHex(32):        "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://192.0.2.1&url=https://[2001:db8::1]&code=jh_00": "bad_code",
		"persona://partner?hub=..%2Fx&fp=x&url=https://192.0.2.1&code=jh_" + randomHex(32):                   "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://user:pw@192.0.2.1&code=jh_" + randomHex(32):     "bad_code",
	} {
		if _, err := c.h.joinPartner(ctx, raw); partnerErrCode(err) != want || partnerErrStatus(err) != http.StatusBadRequest {
			t.Errorf("%s: %v", raw, err)
		}
	}
	// Over the apps' route: the code's own errors with their codes.
	full, _ := c.deviceToken(t, roleFull)
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs", full, `{"code":"persona://partner?hub=x"}`); st != 400 || !strings.Contains(string(body), `"bad_code"`) {
		t.Fatalf("bad code over the route: %d %s", st, body)
	}
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs", full, `{"code":`+strconvQuote(expired.Code)+`}`); st != 401 || !strings.Contains(string(body), `"code_refused"`) {
		t.Fatalf("used code over the route: %d %s", st, body)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func partnerErrCode(err error) string {
	var pe *partnerError
	if errors.As(err, &pe) {
		return pe.code
	}
	return ""
}

func partnerErrStatus(err error) int {
	var pe *partnerError
	if errors.As(err, &pe) {
		return pe.status
	}
	return 0
}

// Who may not pair: a hub with partner hubs off (either side), a hub without TLS (it cannot hand out a code, but it
// can use one), a hub that is a mesh peer of the other already, a hub that is a mesh member.
func TestPartnerPairingRefusals(t *testing.T) {
	off := false
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{partners: PartnersConfig{Enabled: &off}})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{noTLS: true})
	ctx := context.Background()
	if _, err := a.h.newPartnerCode(ctx); partnerErrCode(err) != "partners_off" || partnerErrStatus(err) != http.StatusForbidden {
		t.Fatalf("code with partners off: %v", err)
	}
	code, _ := b.h.newPartnerCode(ctx)
	if _, err := a.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "partners_off" {
		t.Fatalf("join with partners off: %v", err)
	}
	if _, err := c.h.newPartnerCode(ctx); partnerErrCode(err) != "no_tls" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("code without TLS: %v", err)
	}
	full, _ := c.deviceToken(t, roleFull)
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs/code", full, ""); st != 409 || !strings.Contains(string(body), `"no_tls"`) {
		t.Fatalf("code route without TLS: %d %s", st, body)
	}
	if st, body := c.do(t, "GET", "/api/v1/partner-hubs", full, ""); st != 200 || !strings.Contains(string(body), `"can_issue":false`) {
		t.Fatalf("list without TLS: %d %s", st, body)
	}
	offFull, _ := a.deviceToken(t, roleFull)
	if st, body := a.do(t, "GET", "/api/v1/partner-hubs", offFull, ""); st != 200 || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("list with partners off: %d %s", st, body)
	}
	// A hub without TLS uses B's code (it dials; B stores it to wait for it, dial never).
	if _, err := c.h.joinPartner(ctx, code.Code); err != nil {
		t.Fatalf("join without TLS: %v", err)
	}
	if row, _ := b.h.partnerRow(hubC); row.Dial != assistantwire.PartnerDialNever || row.FP != "" {
		t.Fatalf("B keeps a hub without TLS as %+v", row)
	}
	// A mesh peer of the other hub: refused by the hub that has it (B, answering C's code with already_linked, which C
	// passes on), and by the one that would join.
	d := startPartnerHub(t, "dddddddd-0000-4000-8000-000000000004", "Mesh", partnerHubOpts{})
	b.h.mesh.mu.Lock()
	b.h.mesh.peers[d.id()] = db.Peer{HubID: d.id(), Name: "Mesh"}
	b.h.mesh.mu.Unlock()
	code2, _ := b.h.newPartnerCode(ctx)
	if _, err := d.h.joinPartner(ctx, code2.Code); partnerErrCode(err) != "already_linked" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("a mesh peer pairing as a partner: %v", err)
	}
	code3, _ := d.h.newPartnerCode(ctx)
	if _, err := b.h.joinPartner(ctx, code3.Code); partnerErrCode(err) != "already_linked" {
		t.Fatalf("pairing a mesh peer as a partner: %v", err)
	}
	// A mesh member has none: it refuses codes and pairing, and answers the hub-to-hub routes itself (member_hub),
	// never passing them on to its center.
	d.h.mesh.mu.Lock()
	d.h.mesh.group = meshGroup{Center: hubB, Term: 1}
	d.h.mesh.mu.Unlock()
	if _, err := d.h.newPartnerCode(ctx); partnerErrCode(err) != "member_hub" {
		t.Fatalf("a member's code: %v", err)
	}
	for _, path := range []string{partnerLinkPath, partnerPairPath} {
		if !d.h.servedByMember(httptest.NewRequest("GET", path, nil)) {
			t.Errorf("a member passes %s on to its center", path)
		}
	}
	if _, err := d.h.joinPartner(ctx, code2.Code); partnerErrCode(err) != "member_hub" {
		t.Fatalf("a member joining: %v", err)
	}
	if d.h.partnersOn() || len(d.h.partnerFeatures()) != 0 {
		t.Fatal("partner hubs on on a mesh member")
	}
}

// What another hub says about itself is bounded: a hub_id that is not one, a token or pin of any size, more addresses
// than are ever dialed. The pairing route refuses the first ones; addresses are cut to partnerMaxEndpoints, https only,
// without credentials in them.
func TestPartnerPairBounds(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	ctx := context.Background()
	pair := func(body string) (int, []byte) {
		t.Helper()
		code, err := b.h.newPartnerCode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parsePartnerCode(code.Code)
		if err != nil {
			t.Fatal(err)
		}
		return b.do(t, "POST", partnerPairPath, parsed.secret, body)
	}
	for _, body := range []string{
		`{"hub_id":"../x","token":"jd_` + randomHex(24) + `"}`,
		`{"hub_id":"` + strings.Repeat("a", 65) + `","token":"jd_` + randomHex(24) + `"}`,
		`{"hub_id":"` + hubA + `","token":"jd_` + strings.Repeat("0", 300) + `"}`,
		`{"hub_id":"` + hubA + `","fp":"` + strings.Repeat("x", 200) + `","token":"jd_` + randomHex(24) + `"}`,
	} {
		if code, out := pair(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if _, ok := b.h.partnerRow(hubA); ok {
		t.Fatal("a refused pairing was kept")
	}
	var eps []string
	for i := range 40 {
		eps = append(eps, "https://hub"+strconv.Itoa(i)+".example.com:18662")
	}
	eps = append(eps, "http://192.0.2.9:18660", "https://user:pw@192.0.2.10", "https://"+strings.Repeat("a", 600)+".example.com")
	body, _ := json.Marshal(partnerInfo{HubID: hubA, Name: "家里", FP: "fp", Endpoints: eps, Token: "jd_" + randomHex(24)})
	if code, out := pair(string(body)); code != http.StatusOK {
		t.Fatalf("pair: %d %s", code, out)
	}
	row, _ := b.h.partnerRow(hubA)
	if len(row.Endpoints) != partnerMaxEndpoints {
		t.Fatalf("kept %d addresses: %v", len(row.Endpoints), row.Endpoints)
	}
	for _, e := range row.Endpoints {
		if !strings.HasPrefix(e, "https://hub") {
			t.Fatalf("kept %q", e)
		}
	}
}

// A name another hub gives is kept as one plain line: no control or bidirectional formatting characters, 64 at most.
func TestPartnerName(t *testing.T) {
	for in, want := range map[string]string{
		"  Office\n hub ":       "Office hub",
		"Off\u202eice\x1b[31m":  "Office[31m",
		"家里\u0000":              "家里",
		strings.Repeat("长", 70): strings.Repeat("长", 64) + "…",
	} {
		if got := partnerName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The other hub's refusal: its code passed on when the apps know it, "refused" when not, its words on one short line.
func TestPartnerAnswered(t *testing.T) {
	long := strings.Repeat("why ", 400)
	for _, tc := range []struct {
		status    int
		body      string
		code      string
		statusOut int
	}{
		{401, `{"error":"no"}`, "code_refused", http.StatusUnauthorized},
		{409, `{"error":"linked","code":"already_linked"}`, "already_linked", http.StatusConflict},
		{409, `{"error":"x","code":"` + strings.Repeat("z", 500) + `"}`, "refused", http.StatusBadGateway},
		{500, `{"error":"` + long + `\nsecond line"}`, "refused", http.StatusBadGateway},
	} {
		err := partnerAnswered(tc.status, []byte(tc.body))
		if partnerErrCode(err) != tc.code || partnerErrStatus(err) != tc.statusOut {
			t.Errorf("%d %s: %v (%s, %d)", tc.status, clip(tc.body, 60), err, partnerErrCode(err), partnerErrStatus(err))
		}
		if text := err.Error(); len(text) > 400 || strings.Contains(text, "\n") {
			t.Errorf("the other hub's words not bounded: %q", text)
		}
	}
}
