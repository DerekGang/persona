package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// botmuxCapsTTL bounds how long a probed BotMux instance capability record is reused without re-probing,
// covering an in-place upgrade behind a wrapper script whose file stamp stays unchanged.
const (
	botmuxCapsTTL       = 5 * time.Minute
	botmuxTurnsPageSize = 100
	botmuxTurnsMaxPages = 20
	botmuxTurnsMaxBytes = 8 << 20
)

// botmuxCaps caches what a BotMux dashboard instance supports, keyed by its loopback URL, token, and CLI file stamp.
// A dashboard restart (new URL/token) or CLI upgrade (new stamp) invalidates the cache automatically.
type botmuxCaps struct {
	known      bool
	turnsAPI   bool
	ownerActor bool
	base       string
	token      string
	stamp      string
	at         time.Time
}

func (c botmuxCaps) valid(base, token, stamp string, now time.Time) bool {
	return c.known && c.base == base && c.token == token && c.stamp == stamp && now.Sub(c.at) < botmuxCapsTTL
}

type botmuxSessionTurnsCache struct {
	base   string
	token  string
	stamp  string
	cursor string
	turns  []transcript.BotmuxTurn
	byID   map[string]int
}

func (h *botmuxHost) externalTriggerNotice() bool {
	stamp := botmuxStamp(h.bin)
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.caps.known || h.caps.ownerActor {
		return false
	}
	if h.caps.stamp != stamp || now.Sub(h.caps.at) >= botmuxCapsTTL {
		return false
	}
	if h.base != "" && (h.caps.base != h.base || h.caps.token != h.token) {
		return false
	}
	return true
}

func (h *botmuxHost) setCapsLocked(next botmuxCaps) (noticeChanged bool) {
	prevNotice := h.caps.known && !h.caps.ownerActor
	h.caps = next
	nextNotice := h.caps.known && !h.caps.ownerActor
	return prevNotice != nextNotice
}

func (h *botmuxHost) noteOwnerActorUnechoed(base, token, stamp string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setCapsLocked(botmuxCaps{
		known:      true,
		turnsAPI:   h.caps.turnsAPI,
		ownerActor: false,
		base:       base,
		token:      token,
		stamp:      stamp,
		at:         time.Now(),
	})
}

// probeCaps checks what this BotMux instance supports using only the read-only GET /api/sessions/:id/turns endpoint.
// Never sends a real message and never retries a write.
func (h *botmuxHost) probeCaps(ctx context.Context, sessionID string) botmuxCaps {
	if sessionID == "" {
		return botmuxCaps{}
	}
	stamp := botmuxStamp(h.bin)
	now := time.Now()
	base, token, err := h.dashboard(ctx, false)
	if err != nil {
		return botmuxCaps{}
	}
	h.mu.Lock()
	if h.caps.valid(base, token, stamp, now) {
		c := h.caps
		h.mu.Unlock()
		return c
	}
	h.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			base, token, err = h.dashboard(ctx, true)
			if err != nil {
				return botmuxCaps{}
			}
			h.mu.Lock()
			if h.caps.valid(base, token, stamp, time.Now()) {
				c := h.caps
				h.mu.Unlock()
				return c
			}
			h.mu.Unlock()
		}
		status, res, errCode, err := h.fetchTurnsPage(ctx, base, token, sessionID, "0", 1)
		stale := err != nil || status == http.StatusUnauthorized || status == http.StatusForbidden ||
			(status >= 300 && status < 400)
		if stale && attempt == 0 {
			continue
		}
		if err != nil {
			return botmuxCaps{}
		}
		switch status {
		case http.StatusNotFound:
			// New BotMux returns 404 {"ok":false,"error":"unknown_session"} when the route exists
			// but the session is not currently owned by an online daemon; do not cache that as an old BotMux.
			if errCode == "unknown_session" {
				return botmuxCaps{}
			}
			c := botmuxCaps{known: true, turnsAPI: false, ownerActor: false, base: base, token: token, stamp: stamp, at: time.Now()}
			h.mu.Lock()
			h.setCapsLocked(c)
			h.mu.Unlock()
			return c
		case http.StatusOK:
			supported := res.OK && res.Schema == transcript.BotmuxTurnsSchema
			c := botmuxCaps{
				known:      true,
				turnsAPI:   supported,
				ownerActor: supported && slices.Contains(res.Capabilities, transcript.BotmuxCapOwnerActor),
				base:       base,
				token:      token,
				stamp:      stamp,
				at:         time.Now(),
			}
			h.mu.Lock()
			h.setCapsLocked(c)
			h.mu.Unlock()
			return c
		default:
			return botmuxCaps{}
		}
	}
	return botmuxCaps{}
}

// sessionTurns returns the cached + incrementally fetched structured turns for sessionID.
// Returns (nil, capsChanged) when the BotMux instance does not support the endpoint or cannot be reached.
func (h *botmuxHost) sessionTurns(ctx context.Context, sessionID string) ([]transcript.BotmuxTurn, bool) {
	if sessionID == "" {
		return nil, false
	}
	stamp := botmuxStamp(h.bin)
	now := time.Now()
	base, token, err := h.dashboard(ctx, false)
	if err != nil {
		return nil, false
	}
	h.mu.Lock()
	if h.caps.valid(base, token, stamp, now) && !h.caps.turnsAPI {
		h.mu.Unlock()
		return nil, false
	}
	cursor := "0"
	if c := h.turnsCache[sessionID]; c != nil && c.base == base && c.token == token && c.stamp == stamp && c.cursor != "" {
		cursor = c.cursor
	}
	h.mu.Unlock()

	refreshed := false
	noticeChanged := false
	for page := 0; page < botmuxTurnsMaxPages; page++ {
		status, res, errCode, err := h.fetchTurnsPage(ctx, base, token, sessionID, cursor, botmuxTurnsPageSize)
		stale := err != nil || status == http.StatusUnauthorized || status == http.StatusForbidden ||
			(status >= 300 && status < 400)
		if stale && !refreshed {
			refreshed = true
			base, token, err = h.dashboard(ctx, true)
			if err != nil {
				return nil, noticeChanged
			}
			cursor = "0"
			page--
			continue
		}
		if err != nil {
			break
		}
		if status == http.StatusNotFound {
			if errCode != "unknown_session" {
				h.mu.Lock()
				if h.setCapsLocked(botmuxCaps{known: true, turnsAPI: false, ownerActor: false, base: base, token: token, stamp: stamp, at: time.Now()}) {
					noticeChanged = true
				}
				h.mu.Unlock()
			}
			return nil, noticeChanged
		}
		if status != http.StatusOK {
			break
		}
		if !res.OK || res.Schema != transcript.BotmuxTurnsSchema {
			h.mu.Lock()
			if h.setCapsLocked(botmuxCaps{known: true, turnsAPI: false, ownerActor: false, base: base, token: token, stamp: stamp, at: time.Now()}) {
				noticeChanged = true
			}
			h.mu.Unlock()
			return nil, noticeChanged
		}

		h.mu.Lock()
		if h.setCapsLocked(botmuxCaps{
			known:      true,
			turnsAPI:   true,
			ownerActor: slices.Contains(res.Capabilities, transcript.BotmuxCapOwnerActor),
			base:       base,
			token:      token,
			stamp:      stamp,
			at:         time.Now(),
		}) {
			noticeChanged = true
		}
		if h.turnsCache == nil {
			h.turnsCache = map[string]*botmuxSessionTurnsCache{}
		}
		sc := h.turnsCache[sessionID]
		if sc == nil || sc.base != base || sc.token != token || sc.stamp != stamp {
			sc = &botmuxSessionTurnsCache{
				base:  base,
				token: token,
				stamp: stamp,
				byID:  map[string]int{},
			}
			h.turnsCache[sessionID] = sc
		}
		for _, turn := range res.Turns {
			if idx, ok := sc.byID[turn.TurnID]; ok {
				sc.turns[idx] = turn
			} else {
				sc.byID[turn.TurnID] = len(sc.turns)
				sc.turns = append(sc.turns, turn)
			}
		}
		if res.Next != "" {
			sc.cursor = res.Next
		}
		h.mu.Unlock()

		if len(res.Turns) < botmuxTurnsPageSize || res.Next == "" || res.Next == cursor {
			break
		}
		cursor = res.Next
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	sc := h.turnsCache[sessionID]
	if sc == nil || len(sc.turns) == 0 {
		return nil, noticeChanged
	}
	return slices.Clone(sc.turns), noticeChanged
}

func (h *botmuxHost) fetchTurnsPage(
	ctx context.Context, base, token, sessionID, since string, limit int,
) (status int, res transcript.BotmuxTurnsResponse, errCode string, err error) {
	u := fmt.Sprintf("%s/api/sessions/%s/turns?since=%s&limit=%d",
		base, url.PathEscape(sessionID), url.QueryEscape(since), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, res, "", err
	}
	req.AddCookie(&http.Cookie{Name: "botmux_dashboard_token", Value: token})
	resp, err := h.client.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return 0, res, "", fmt.Errorf("botmux: the dashboard does not answer: %w", err)
		}
		return 0, res, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, botmuxTurnsMaxBytes))
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &res); err != nil {
			return resp.StatusCode, res, "", err
		}
		return resp.StatusCode, res, "", nil
	}
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(b, &errBody)
	return resp.StatusCode, res, errBody.Error, nil
}

// botmuxSessionID returns the BotMux session ID when sessionID belongs to a BotMux session, or "" otherwise.
func (n *Node) botmuxSessionID(sessionID string) string {
	if a, ok := n.agent(sessionID); ok {
		if ref := host.ParsePane(a.PaneID); ref.Kind == host.Botmux {
			return ref.ID
		}
	}
	return ""
}

// applyBotmuxTurns overlays structured BotMux turn records onto items for a BotMux session.
// Non-BotMux sessions return items untouched with no network calls.
func (n *Node) applyBotmuxTurns(ctx context.Context, sessionID string, items []proto.Item) []proto.Item {
	if len(items) == 0 {
		return items
	}
	bmxSID := n.botmuxSessionID(sessionID)
	if bmxSID == "" {
		return items
	}
	h, ok := n.hosts.Get(host.Botmux).(*botmuxHost)
	if !ok {
		return items
	}
	turns, _ := h.sessionTurns(ctx, bmxSID)
	if len(turns) == 0 {
		return items
	}
	return transcript.ApplyBotmuxTurns(items, turns)
}
