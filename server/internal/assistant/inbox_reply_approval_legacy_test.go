package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func legacyApprovalDraft(it *InboxItem) {
	g := it.Suggestion
	it.Agent = &InboxAgent{State: AgentDrafted, Draft: g.Text, Note: g.Note, DraftedAt: &g.At}
	it.Suggestion = nil
}

func TestInboxReplyApprovalLegacyWarningSurvivesRestart(t *testing.T) {
	for _, clock := range []string{"draft time", "legacy source time"} {
		t.Run(clock, func(t *testing.T) {
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, f := newQuietServiceOn(t, database)
			holdDigest(s)
			if err := s.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			it := &InboxItem{ID: "legacy-approval", Source: "example", Tier: TierAction, Last: "a",
				UpdatedAt: now.Add(-20 * time.Hour), Suggestion: &InboxSuggestion{
					Version: 1, Kind: SuggestReply, Text: "The prepared answer.", Note: "Original explanation",
					At: now.Add(-4 * time.Hour)},
				Connector: &InboxConnector{Thread: "legacy-thread", URL: "https://example.com/messages/legacy"}}
			legacyApprovalDraft(it)
			if clock == "legacy source time" {
				it.Agent.DraftedAt = nil
			}
			s.inbox.mu.Lock()
			s.inbox.items[it.ID] = it
			s.inbox.mu.Unlock()
			if failed := s.saveInbox(ctx, []*InboxItem{it}); failed[it.ID] {
				t.Fatal("could not store the original legacy draft")
			}
			if clock == "draft time" {
				s.superviseWatch(ctx, now.Add(-time.Nanosecond))
				if it.Urgent {
					t.Fatal("legacy draft was reminded before four hours")
				}
			}
			s.superviseWatch(ctx, now)
			g := currentSuggestion(it)
			if it.Suggestion != nil || g == nil || g.Text != "The prepared answer." ||
				!strings.Contains(g.Note, "Original explanation") || !strings.Contains(g.Note, "20") ||
				!strings.Contains(it.Agent.Note, "20") {
				t.Errorf("legacy draft lost its visible waiting-time warning: agent=%+v, current=%+v", it.Agent, g)
			}
			if !it.Urgent || it.nowKey() != NowConfirmReply ||
				it.Chain[len(it.Chain)-1].Detail != it.Connector.URL {
				t.Errorf("legacy reminder lost its existing Needs you home or source: %+v", it.listed())
			}
			f.mu.Lock()
			pushes := len(f.pushes)
			f.mu.Unlock()
			if pushes != 1 {
				t.Fatalf("legacy draft sent %d owner notifications, want 1", pushes)
			}
			restarted, nextHub := newQuietServiceOn(t, database)
			holdDigest(restarted)
			if err := restarted.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			restarted.inbox.mu.Lock()
			recovered := restarted.inbox.items[it.ID]
			if recovered == nil || recovered.Suggestion != nil || recovered.Agent == nil ||
				recovered.Agent.Draft != "The prepared answer." || !strings.Contains(recovered.Agent.Note, "20") ||
				!strings.Contains(currentSuggestion(recovered).Note, "20") {
				t.Errorf("Postgres reload lost the legacy draft warning: %+v", recovered)
			}
			restarted.inbox.mu.Unlock()
			restarted.superviseWatch(ctx, now.Add(time.Minute))
			nextHub.mu.Lock()
			if len(nextHub.pushes) != 0 {
				t.Error("manager reconstruction duplicated the persisted legacy reminder")
			}
			nextHub.mu.Unlock()
		})
	}
}

func TestInboxReplyApprovalLegacyFailedSaveProtectsNote(t *testing.T) {
	for _, replacement := range []string{"unchanged", "new note", "new agent"} {
		t.Run(replacement, func(t *testing.T) {
			s, _, it, st, now := approvalBlockedFixture(t, 1)
			legacyApprovalDraft(it)
			done := make(chan struct{})
			go func() {
				s.checkInboxReplyApprovals(context.Background(), now)
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			stagedWarning := strings.Contains(it.Agent.Note, "20")
			switch replacement {
			case "new note":
				it.Agent.Note = "New explanation"
			case "new agent":
				it.Agent = &InboxAgent{State: AgentDrafted, Draft: "New draft", Note: "New explanation", DraftedAt: &now}
			}
			want, _ := json.Marshal(it)
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			if !stagedWarning {
				t.Error("reminder edited a temporary suggestion instead of the legacy note")
			}
			if replacement == "unchanged" {
				if it.Agent.Note != "Original note" || it.Urgent || it.Reason != "Original reason" {
					t.Errorf("failed legacy reminder did not restore its own note and urgency: %+v", it)
				}
				s.checkInboxReplyApprovals(context.Background(), now)
				if !strings.Contains(it.Agent.Note, "20") || !it.Urgent {
					t.Error("retry did not persist the legacy waiting-time warning")
				}
			} else {
				got, _ := json.Marshal(it)
				if string(got) != string(want) {
					t.Errorf("failed legacy reminder overwrote a replacement:\nwant %s\n got %s", want, got)
				}
			}
		})
	}
}
