package assistant

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

const inboxReplyApprovalAfter = 4 * time.Hour

type inboxReplyApproval struct {
	Version        int    `json:"version"`
	Notified       bool   `json:"notified,omitempty"`
	PreviousUrgent bool   `json:"previous_urgent,omitempty"`
	PreviousReason string `json:"previous_reason,omitempty"`
	Explanation    string `json:"explanation"`
}

// Drafts already have one home in Inbox Needs you. Make an old one urgent there,
// with a source-linked explanation, and notify the owner; never create a second
// decision, change the draft text, or send it to the other person.
func (s *Service) checkInboxReplyApprovals(ctx context.Context, now time.Time) {
	b := &s.inbox
	if !b.loaded.Load() {
		return
	}
	s.clearStaleReplyApprovals(ctx)
	type reminder struct {
		item         *InboxItem
		version      int
		at           time.Time
		text, url    string
		oldUrgent    bool
		oldReason    string
		oldReceipt   *inboxReplyApproval
		oldNote      string
		chainID      string
		conversation string
		displayed    bool
	}
	var reminders []reminder
	b.mu.Lock()
	for _, it := range b.items {
		g := currentSuggestion(it)
		receipt := it.ReplyApproval
		if !it.open() || it.Tier == TierNoise || g == nil || g.Kind != SuggestReply ||
			g.At.IsZero() || now.Before(g.At.Add(inboxReplyApprovalAfter)) ||
			receipt != nil && receipt.Version == g.Version && receipt.Notified {
			continue
		}
		since := g.At
		if !it.UpdatedAt.IsZero() && it.UpdatedAt.Before(since) {
			since = it.UpdatedAt
		}
		hours := max(0, int(now.Sub(since)/time.Hour))
		text := i18n.T(b.userLang(), "assistant-commitments.approval_overdue", i18n.Args{"hours": hours})
		source := sourceLink(it)
		link := source.URL
		if link == "" {
			link = "/inbox/" + it.ID
		}
		r := reminder{item: it, version: g.Version, at: g.At, text: text, url: link,
			oldUrgent: it.Urgent, oldReason: it.Reason, oldReceipt: receipt, oldNote: g.Note}
		if g.Conversation != nil {
			r.conversation = g.Conversation.SessionID
		} else if it.Agent != nil {
			r.conversation = it.Agent.SessionID
		}
		if receipt == nil || receipt.Version != g.Version {
			it.ReplyApproval = &inboxReplyApproval{Version: g.Version, PreviousUrgent: it.Urgent,
				PreviousReason: it.Reason, Explanation: text}
			it.Urgent, it.Reason = true, text
			g.Note = g.Note + "\n" + text
			b.chainAdd(it, InboxChainEntry{Kind: ChainCheck, At: now.UTC(), Text: text, Detail: link,
				Link: firstLink(it), Via: ViaAssistant})
			r.chainID = it.Chain[len(it.Chain)-1].ID
			r.displayed = true
		}
		reminders = append(reminders, r)
	}
	b.mu.Unlock()
	items := make([]*InboxItem, 0, len(reminders))
	for _, r := range reminders {
		if r.displayed {
			items = append(items, r.item)
		}
	}
	failed := s.saveInboxUndo(ctx, items, func(failed map[string]bool) {
		for _, r := range reminders {
			it := r.item
			g := currentSuggestion(it)
			if !failed[it.ID] || g == nil || g.Version != r.version || !g.At.Equal(r.at) ||
				it.ReplyApproval == nil || it.ReplyApproval.Version != r.version {
				continue
			}
			it.Urgent, it.Reason, it.ReplyApproval = r.oldUrgent, r.oldReason, r.oldReceipt
			g.Note = r.oldNote
			it.Chain = slices.DeleteFunc(it.Chain, func(e InboxChainEntry) bool { return e.ID == r.chainID })
		}
	})
	for _, r := range reminders {
		if failed[r.item.ID] {
			continue
		}
		b.mu.Lock()
		g := currentSuggestion(r.item)
		current := r.item.open() && r.item.Tier != TierNoise && g != nil && g.Kind == SuggestReply &&
			g.Version == r.version && g.At.Equal(r.at) && r.item.ReplyApproval != nil && r.item.ReplyApproval.Version == r.version
		b.mu.Unlock()
		if !current {
			continue
		}
		// The stable notification id lets the hub deduplicate a repeated receipt.
		_, err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: Notification{
			ID: fmt.Sprintf("inbox-reply-%s-%d", r.item.ID, r.version), Kind: NotifyPush,
			Title: s.outboundText(ctx, "approval_title", nil), Body: r.text + "\n" + r.url,
			SessionID: r.conversation}})
		if err != nil {
			s.logf("overdue Inbox reply notification: %v", err)
			continue
		}
		b.mu.Lock()
		g = currentSuggestion(r.item)
		current = r.item.open() && r.item.Tier != TierNoise && g != nil && g.Kind == SuggestReply &&
			g.Version == r.version && g.At.Equal(r.at) && r.item.ReplyApproval != nil && r.item.ReplyApproval.Version == r.version
		if current {
			receipt := *r.item.ReplyApproval
			receipt.Notified = true
			r.item.ReplyApproval = &receipt
		}
		b.mu.Unlock()
		if current {
			s.saveInboxUndo(ctx, []*InboxItem{r.item}, func(failed map[string]bool) {
				if failed[r.item.ID] && r.item.ReplyApproval != nil && r.item.ReplyApproval.Version == r.version {
					receipt := *r.item.ReplyApproval
					receipt.Notified = false
					r.item.ReplyApproval = &receipt
				}
			})
		}
	}
}

func (s *Service) clearStaleReplyApprovals(ctx context.Context) {
	b := &s.inbox
	type cleared struct {
		it      *InboxItem
		receipt *inboxReplyApproval
		urgent  bool
		reason  string
	}
	var clearedRows []cleared
	var items []*InboxItem
	b.mu.Lock()
	for _, it := range b.items {
		r := it.ReplyApproval
		g := currentSuggestion(it)
		if r == nil || it.open() && it.Tier != TierNoise && g != nil && g.Kind == SuggestReply && g.Version == r.Version {
			continue
		}
		clearedRows = append(clearedRows, cleared{it, r, it.Urgent, it.Reason})
		if it.Reason == r.Explanation {
			it.Urgent, it.Reason = r.PreviousUrgent, r.PreviousReason
		}
		it.ReplyApproval = nil
		items = append(items, it)
	}
	b.mu.Unlock()
	s.saveInboxUndo(ctx, items, func(failed map[string]bool) {
		for _, r := range clearedRows {
			if failed[r.it.ID] && r.it.ReplyApproval == nil &&
				(r.it.Reason == r.receipt.PreviousReason || r.it.Reason == r.reason) {
				r.it.ReplyApproval, r.it.Urgent, r.it.Reason = r.receipt, r.urgent, r.reason
			}
		}
	})
}
