package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// hub_list and hub_send (prompts/mcp/assistant), and the owner's card for a message to a partner hub. hub_send never
// holds the conversation's turn: it keeps the message and returns, the card goes up from a goroutine of its own, and
// what becomes of the message comes back into the conversation as a notice (partnerTell) or the partner's reply.

// partnerCaller refuses a conversation that may not message partner hubs. Only the assistant's own conversations (the
// main one and the others of its space) may: not the health space's, a guest's, one that writes to other people, a
// meeting's Just me turn, nor anything on a work computer.
func (s *Service) partnerCaller(ctx context.Context, machine, self string) error {
	if self == "" {
		return errors.New("only a Persona conversation can message partner hubs")
	}
	if isOutwardReader(ctx) {
		return errors.New("a conversation that writes to other people does not message partner hubs")
	}
	if s.isWorkMachine(machine) {
		return errors.New("a conversation on a work computer does not message partner hubs")
	}
	if _, guest, err := s.guestOf(ctx, machine, self); err != nil || guest {
		return errors.New("a guest conversation does not message partner hubs")
	}
	if s.justMeNow(machine, self) {
		return errors.New("this is a Just me turn of a meeting: nothing of it goes to partner hubs")
	}
	c, ok := s.session(ctx, machine, self)
	switch {
	case !ok || !c.Assistant:
		return errors.New("only the assistant's own conversations can message partner hubs")
	case spaceOf(c) == SpaceHealth:
		return errors.New("the health space doesn't message partner hubs: health information stays in its conversations")
	case spaceOf(c) != "":
		return errors.New("only the assistant's own conversations can message partner hubs")
	}
	return nil
}

// partnerAsks says, for a paired hub, whether a message to it waits for the owner's OK (no setting, or one that
// cannot be read: it does).
func partnerAsks(ctx context.Context, st partnerStore) func(hub string) bool {
	prefs, err := st.PartnerPrefs(ctx)
	off := map[string]bool{}
	if err == nil {
		for _, p := range prefs {
			off[p.HubID] = !p.Confirm
		}
	}
	return func(hub string) bool { return !off[hub] }
}

func (s *Service) toolHubList(ctx context.Context, machine, self string) (string, error) {
	st := s.partnerStore()
	if st == nil {
		return "", errPartnersOff
	}
	if err := s.partnerCaller(ctx, machine, self); err != nil {
		return "", err
	}
	list, err := s.partnerList(ctx)
	if err != nil {
		return "", fmt.Errorf("the partner hubs could not be read now: %w", err)
	}
	if !list.Enabled {
		return "Partner hubs are off on this hub.", nil
	}
	if len(list.Hubs) == 0 {
		return "No partner hub is paired with this hub.", nil
	}
	asks := partnerAsks(ctx, st)
	var b strings.Builder
	b.WriteString("Partner hubs (hub_send messages their assistants):")
	for _, h := range list.Hubs {
		state := "offline (a message waits until it is online)"
		if h.Online {
			state = "online"
		}
		confirm := "a message waits for the user's OK on the phone first"
		if !asks(h.HubID) {
			confirm = "a message goes without asking the user"
		}
		fmt.Fprintf(&b, "\n- %q (hub_id %s): %s; %s", h.Name, h.HubID, state, confirm)
	}
	return b.String(), nil
}

// partnerHubFor is the paired hub hub names: its hub_id, else its name if no other has it.
func (s *Service) partnerHubFor(ctx context.Context, hub string) (assistantwire.PartnerHub, error) {
	list, err := s.partnerList(ctx)
	if err != nil {
		return assistantwire.PartnerHub{}, fmt.Errorf("the partner hubs could not be read now: %w", err)
	}
	if !list.Enabled {
		return assistantwire.PartnerHub{}, errors.New("partner hubs are off on this hub")
	}
	hub = strings.TrimSpace(hub)
	if hub == "" {
		return assistantwire.PartnerHub{}, errors.New("hub is required (hub_list lists the partner hubs)")
	}
	var named []assistantwire.PartnerHub
	for _, h := range list.Hubs {
		if h.HubID == hub {
			return h, nil
		}
		if strings.EqualFold(strings.TrimSpace(h.Name), hub) {
			named = append(named, h)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return assistantwire.PartnerHub{}, fmt.Errorf("no partner hub %q (hub_list lists them)", hub)
	}
	ids := make([]string, len(named))
	for i, h := range named {
		ids[i] = h.HubID
	}
	return assistantwire.PartnerHub{}, fmt.Errorf("%q is the name of %d partner hubs: give its hub_id (%s)", hub, len(named), strings.Join(ids, ", "))
}

// partnerTokenRe is a credential of Persona's own (a device's, a pairing code, a bus's or a connector's token, an
// extension's) that SanitizeShareText does not know.
var partnerTokenRe = lazyre.New(`\b(?:(?:jd|jp|jx|jb|jc|jh)_[A-Za-z0-9_-]{16,}|pext_[A-Za-z0-9_-]{8,})`)

// partnerScrub is text as it may leave for a partner hub: credentials removed (transcript.SanitizeShareText, and
// Persona's own token kinds), and how many places were. A text the sanitizer leaves as it was, but for its Unicode
// normalisation, goes as written: that normalisation alone would change the punctuation of Chinese text.
func partnerScrub(text string) (string, int, error) {
	text = strings.TrimSpace(text)
	clean, err := transcript.SanitizeShareText(text)
	if err != nil {
		return "", 0, err
	}
	removed := 0
	if clean == norm.NFKC.String(strings.ReplaceAll(text, "\r\n", "\n")) {
		clean = text
	} else {
		removed = max(strings.Count(clean, "[REDACTED")-strings.Count(text, "[REDACTED"), 1)
	}
	clean = partnerTokenRe.ReplaceAllStringFunc(clean, func(string) string {
		removed++
		return "[REDACTED]"
	})
	return strings.TrimSpace(clean), removed, nil
}

func newPartnerMessageID() string { return "pm_" + randomHex(16) }

func (s *Service) toolHubSend(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	var a struct {
		Hub     string `json:"hub"`
		Text    string `json:"text"`
		ReplyTo string `json:"reply_to"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid hub_send arguments: %w", err)
	}
	st := s.partnerStore()
	if st == nil {
		return "", errPartnersOff
	}
	if err := s.partnerCaller(ctx, machine, self); err != nil {
		return "", err
	}
	hub, err := s.partnerHubFor(ctx, a.Hub)
	if err != nil {
		return "", err
	}
	text, removed, err := partnerScrub(a.Text)
	if err != nil {
		return "", fmt.Errorf("the text can't be sent: %w", err)
	}
	if text == "" {
		return "", errors.New("text is empty")
	}
	if n := utf8.RuneCountInString(text); n > assistantwire.PartnerTextMax {
		return "", fmt.Errorf("text is %d characters; a message to a partner hub carries at most %d: shorten it", n, assistantwire.PartnerTextMax)
	}
	kind, hop := assistantwire.PartnerKindMessage, 1
	if replyTo := strings.TrimSpace(a.ReplyTo); replyTo != "" {
		in, err := st.PartnerMessageOf(ctx, partnerIn, hub.HubID, replyTo)
		if err != nil {
			return "", err
		}
		if in == nil || in.Kind == assistantwire.PartnerKindStatus {
			return "", fmt.Errorf("reply_to %q names no message %q sent here", replyTo, hub.Name)
		}
		if hop = in.Hop + 1; hop > assistantwire.PartnerHopMax {
			return "", fmt.Errorf("this exchange with %q is %d messages long already (at most %d): tell the user instead of answering", hub.Name, in.Hop, assistantwire.PartnerHopMax)
		}
		kind, a.ReplyTo = assistantwire.PartnerKindReply, replyTo
	} else {
		a.ReplyTo = ""
	}
	now := time.Now().UTC()
	if n, err := st.CountPartnerMessages(ctx, partnerOut, hub.HubID, false, now.Add(-partnerRateWindow)); err != nil {
		return "", err
	} else if n >= partnerOutRate {
		return "", fmt.Errorf("%d messages went to %q in the last %d minutes already (at most %d): wait, or tell the user", n, hub.Name, int(partnerRateWindow.Minutes()), partnerOutRate)
	}
	asks := partnerAsks(ctx, st)(hub.HubID)
	m := db.PartnerMessage{ID: "pr_" + randomHex(12), Direction: partnerOut, PeerHub: hub.HubID, PeerName: partnerInline(hub.Name, 64),
		MessageID: newPartnerMessageID(), Kind: kind, ReplyTo: a.ReplyTo, Hop: hop, MachineID: machine, SessionID: self,
		Title: s.partnerTitle(ctx, machine, self), Text: text, State: partnerConfirming, CreatedAt: now}
	if !asks {
		m.State, m.NextTry = partnerQueued, &now
	}
	m, _, err = st.InsertPartnerMessage(ctx, m)
	if err != nil {
		return "", err
	}
	note := ""
	if removed > 0 {
		note = fmt.Sprintf(" Credentials were removed from the text first (%d found); what goes is the text without them.", removed)
	}
	if asks {
		go s.partnerAsk(m)
		return fmt.Sprintf("Waiting for your OK on your phone; it goes to %q after you allow it, and its reply arrives here (message_id %s). Don't send it again.%s",
			hub.Name, m.MessageID, note), nil
	}
	s.partnerWakeUp()
	return fmt.Sprintf("Sent to the queue for %q; it goes as soon as that hub can take it, and its reply arrives here (message_id %s). Don't send it again.%s",
		hub.Name, m.MessageID, note), nil
}

// partnerTitle is what the sending conversation is called (the messages' list shows it).
func (s *Service) partnerTitle(ctx context.Context, machine, self string) string {
	if c, ok := s.session(ctx, machine, self); ok {
		return conversationName(c, s.settings(ctx).Main)
	}
	return ""
}

// partnerAsk raises the owner's card for confirming message m and acts on the answer. It is the row's only card in
// this process; one the row moved past, or another manager's, is not raised. A service that stops ends the wait with
// the row still confirming: the next start asks again (partnerStart).
func (s *Service) partnerAsk(m db.PartnerMessage) {
	st := s.partnerStore()
	if st == nil {
		return
	}
	b := &s.partners
	b.mu.Lock()
	if b.asking[m.ID] {
		b.mu.Unlock()
		return
	}
	if b.asking == nil {
		b.asking = map[string]bool{}
	}
	b.asking[m.ID] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.asking, m.ID)
		b.mu.Unlock()
	}()
	ctx := s.runCtx()
	now := time.Now().UTC()
	expires := m.CreatedAt.Add(partnerCardWait)
	if !expires.After(now) {
		s.partnerAnswered(ctx, st, m, proto.Resolution{Dismiss: true})
		return
	}
	l := s.lang(ctx)
	input, _ := json.Marshal(map[string]string{"hub": m.PeerName, "hub_id": m.PeerHub, "message_id": m.MessageID, "text": m.Text})
	ix := proto.Interaction{ID: "ix_" + randomHex(6), MachineID: m.MachineID, SessionID: m.SessionID, Kind: proto.IxPermission,
		ToolName: "hub_send", Summary: i18n.T(l, "assistant-partners.confirm", i18n.Args{"hub": m.PeerName}) + "\n\n" + m.Text,
		ToolInput: input, SessionTitle: "Persona", AlwaysLabel: i18n.T(l, "assistant-partners.always", i18n.Args{"hub": m.PeerName}),
		CreatedAt: now, ExpiresAt: expires}
	if c, ok := s.session(ctx, m.MachineID, m.SessionID); ok {
		ix.Cwd = c.Cwd
	}
	m, ok, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerConfirming {
			return false
		}
		r.CardID = ix.ID
		return true
	})
	if err != nil || !ok {
		if err != nil {
			s.logf("partner %s: card: %v", m.MessageID, err)
		}
		return
	}
	r, err := s.ask(ctx, ix)
	if err != nil {
		return // the service stopped: asked again at its next start
	}
	s.partnerAnswered(context.WithoutCancel(ctx), st, m, r)
}

// partnerAnswered acts on the owner's answer r to message m's card: only their own allow lets it go (and an Always
// with it lets every later message to that hub go without a card); a no is declined; a card that ended unanswered, or
// with a yes that was not the owner's, expired. The sending conversation is told when it does not go.
func (s *Service) partnerAnswered(ctx context.Context, st partnerStore, m db.PartnerMessage, r proto.Resolution) {
	allowed := r.UserAllowed()
	next := partnerExpired
	switch {
	case allowed:
		next = partnerQueued
	case r.Allow != nil && !*r.Allow && !r.Dismiss && !r.Cancel:
		next = partnerDeclined
	}
	now := time.Now().UTC()
	_, ok, err := partnerMutate(ctx, st, m, func(row *db.PartnerMessage) bool {
		if row.State != partnerConfirming {
			return false
		}
		row.State = next
		if allowed {
			row.NextTry = &now
		}
		return true
	})
	if err != nil {
		s.logf("partner %s: answer: %v", m.MessageID, err)
	}
	if !ok {
		return // answered on another card, or by another manager
	}
	if allowed {
		if r.Always {
			if err := st.SetPartnerConfirm(ctx, m.PeerHub, false); err != nil {
				s.logf("partner %s: always: %v", m.PeerHub, err)
			}
		}
		s.partnerWakeUp()
		return
	}
	event := "declined"
	if next == partnerExpired {
		event = "expired"
	}
	s.partnerTell(ctx, m, event, "")
}

// partnerTell tells the conversation that sent m (else the main one, when it is gone) what became of it (event: a
// prompts.PartnerNotice event), once.
func (s *Service) partnerTell(ctx context.Context, m db.PartnerMessage, event, code string) {
	machine, to := s.partnerConversation(ctx, m.MachineID, m.SessionID)
	if to == "" {
		s.logf("partner %s: %s, and no conversation to tell", m.MessageID, event)
		return
	}
	text := prompts.RenderPartnerNotice(prompts.PartnerNotice{Language: answerLanguage(s.hello(ctx).Locale), Event: event,
		Hub: m.PeerName, HubID: m.PeerHub, Code: code, Excerpt: partnerInline(m.Text, 120)})
	msg := relayMsg{OnceID: "partner-notice-" + partnerHash(m.ID, event)[:16], From: partnerFrom + m.PeerHub, FromTitle: m.PeerName,
		Tool: partnerNoticeTool, Text: text, At: time.Now().UTC()}
	if err := s.partnerRelay(ctx, machine, to, msg); err != nil && !errors.Is(err, errPartnerRefused) {
		s.logf("partner %s: telling %s: %v", m.MessageID, short(to), err)
	}
}
