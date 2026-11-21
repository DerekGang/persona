package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/feishubot"
)

// Another client wins while resolve is in flight. An ended event on either side
// of the fast result must leave no live buttons or second answer delivery.
func TestReproFastLostPressClosesCard(t *testing.T) {
	for _, during := range []bool{true, false} {
		name := "ended after callback"
		if during {
			name = "ended during resolve"
		}
		t.Run(name, func(t *testing.T) {
			ix := qCard("ix_fast_lost", false, "Beijing", "Shanghai")
			var s *Service
			var ended atomic.Bool
			s, f, _, _, resolves := textAnswerBot(t, func(json.RawMessage) (any, error) {
				ended.Store(true)
				if during {
					s.feishuInteractionDone(ix, "Handled elsewhere")
				}
				return map[string]any{"ok": false}, nil
			}, ix)
			endingQueue(f, ix, &ended, "resolved")
			var updates int
			f.answerApp(AppFeishu, FeishuUpdateCard, func(json.RawMessage) (any, error) {
				updates++
				return map[string]any{}, nil
			})
			seedCard(s, ix)
			ctx := context.Background()
			assertClosed := func(r *feishubot.CardResponse) {
				t.Helper()
				if r == nil || r.Toast == nil || r.Toast.Type != "info" || r.Toast.Content != "已经处理过了" || r.Card == nil {
					t.Fatalf("expected handled toast and closed callback card, got %+v", r)
				}
				raw, err := json.Marshal(r.Card)
				if err != nil || strings.Contains(string(raw), `"callback"`) {
					t.Fatalf("card still has actions: %s (%v)", raw, err)
				}
			}
			assertClosed(s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake"))
			// Redelivery sees the authoritative ended state and cannot resolve again.
			assertClosed(s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake"))
			s.feishuInteractionDone(ix, "Handled elsewhere")
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			f.mu.Lock()
			n, attempts := updates, len(*resolves)
			f.mu.Unlock()
			if tracked || n != 0 || attempts != 1 {
				t.Fatalf("tracked=%v external updates=%d resolve attempts=%d", tracked, n, attempts)
			}
		})
	}
}

func TestFastQuestionPressAcceptedOrRetryable(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "accepted"
		if fails {
			name = "transport error"
		}
		t.Run(name, func(t *testing.T) {
			ix := qCard("ix_fast_answer", false, "Beijing", "Shanghai")
			s, _, _, _, resolves := textAnswerBot(t, func(json.RawMessage) (any, error) {
				if fails {
					return nil, errors.New("temporary transport failure")
				}
				return map[string]any{"ok": true}, nil
			}, ix)
			seedCard(s, ix)
			r := s.botCardAction(context.Background(), ixPress(ix.ID, "Shanghai"), "oa_fake")
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			if r == nil || r.Toast == nil || len(*resolves) != 1 {
				t.Fatalf("response=%+v resolves=%v", r, *resolves)
			}
			if fails {
				if r.Toast.Type != "error" || r.Card != nil || !tracked {
					t.Fatalf("failed answer must remain retryable: response=%+v tracked=%v", r, tracked)
				}
			} else if r.Toast.Type != "success" || r.Card == nil || tracked {
				t.Fatalf("accepted answer must close: response=%+v tracked=%v", r, tracked)
			}
		})
	}
}
