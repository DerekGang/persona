package assistant

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// The apps' partner-hub routes (assistantwire partnerRoutes): the recent messages, and per hub whether a message waits
// for the owner's OK. A full device only: the setting is the owner's, never a tool's (a model must not switch its own
// confirmation off).

const (
	partnerListDefault = 20
	partnerListMost    = 100
)

var partnerHubIDRe = lazyre.New(`^[A-Za-z0-9._:-]{1,128}$`)

func (s *Service) partnerRoutes() {
	s.handle("GET /api/v1/partner-messages", s.getPartnerMessages)
	s.handle("GET /api/v1/partner-messages/prefs", s.getPartnerPrefs)
	s.handle("PUT /api/v1/partner-messages/prefs/{hub_id}", s.putPartnerPref)
}

func partnerErr(w http.ResponseWriter, status int, code string) {
	apierr.WriteCode(w, status, "partner_"+code, i18n.Err("assistant-partners."+code))
}

// partnerRouteStore is the store for a partner route, or the refusal written.
func (s *Service) partnerRouteStore(w http.ResponseWriter, r *http.Request) partnerStore {
	if !fullRole(r) {
		partnerErr(w, http.StatusForbidden, "read_only")
		return nil
	}
	st := s.partnerStore()
	if st == nil {
		partnerErr(w, http.StatusServiceUnavailable, "store")
	}
	return st
}

// partnerMessageView is a message or reply as the apps list it.
func partnerMessageView(m db.PartnerMessage) map[string]any {
	v := map[string]any{"id": m.ID, "direction": m.Direction, "hub_id": m.PeerHub, "hub_name": m.PeerName, "kind": m.Kind,
		"state": m.State, "text": m.Text, "created_at": m.CreatedAt, "updated_at": m.UpdatedAt}
	if m.Code != "" {
		v["code"] = m.Code
	}
	if m.Title != "" {
		v["title"] = m.Title
	}
	return v
}

// getPartnerMessages is GET /api/v1/partner-messages?hub=<hub_id>&limit=<n>: the latest messages and replies, newest
// first.
func (s *Service) getPartnerMessages(w http.ResponseWriter, r *http.Request) {
	st := s.partnerRouteStore(w, r)
	if st == nil {
		return
	}
	q := r.URL.Query()
	hub := strings.TrimSpace(q.Get("hub"))
	limit := partnerListDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			partnerErr(w, http.StatusBadRequest, "invalid")
			return
		}
		limit = min(n, partnerListMost)
	}
	if hub != "" && !partnerHubIDRe.MatchString(hub) {
		partnerErr(w, http.StatusBadRequest, "invalid")
		return
	}
	rows, err := st.RecentPartnerMessages(r.Context(), hub, limit)
	if err != nil {
		partnerErr(w, http.StatusServiceUnavailable, "store")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, partnerMessageView(m))
	}
	apierr.WriteJSON(w, map[string]any{"messages": out})
}

// getPartnerPrefs is GET /api/v1/partner-messages/prefs: the hubs with a setting (any other one asks).
func (s *Service) getPartnerPrefs(w http.ResponseWriter, r *http.Request) {
	st := s.partnerRouteStore(w, r)
	if st == nil {
		return
	}
	prefs, err := st.PartnerPrefs(r.Context())
	if err != nil {
		partnerErr(w, http.StatusServiceUnavailable, "store")
		return
	}
	out := make([]map[string]any, 0, len(prefs))
	for _, p := range prefs {
		out = append(out, map[string]any{"hub_id": p.HubID, "confirm": p.Confirm})
	}
	apierr.WriteJSON(w, map[string]any{"prefs": out})
}

// putPartnerPref is PUT /api/v1/partner-messages/prefs/{hub_id} {"confirm": bool}.
func (s *Service) putPartnerPref(w http.ResponseWriter, r *http.Request) {
	st := s.partnerRouteStore(w, r)
	if st == nil {
		return
	}
	hub := r.PathValue("hub_id")
	var body struct {
		Confirm *bool `json:"confirm"`
	}
	if err := decodeBody(w, r, &body, 4<<10); err != nil {
		writeBodyErr(w, err, i18n.Err("assistant-partners.invalid"))
		return
	}
	if !partnerHubIDRe.MatchString(hub) || body.Confirm == nil {
		partnerErr(w, http.StatusBadRequest, "invalid")
		return
	}
	if err := st.SetPartnerConfirm(r.Context(), hub, *body.Confirm); err != nil {
		partnerErr(w, http.StatusServiceUnavailable, "store")
		return
	}
	apierr.WriteJSON(w, map[string]any{"hub_id": hub, "confirm": *body.Confirm})
}
