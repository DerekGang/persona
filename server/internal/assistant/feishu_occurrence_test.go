package assistant

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestFeishuInteractionActionsCarryOccurrence(t *testing.T) {
	ft := newFleetTestNoStore(t)
	at := time.Date(2026, 11, 21, 1, 2, 3, 456789123, time.UTC)
	honored := true
	card := ft.s.interactionCard(context.Background(), proto.Interaction{
		ID: "ix_feishu_occurrence", MachineID: "mac", SessionID: "s1", Kind: proto.IxPermission,
		CreatedAt: at, CanClarify: true, AlwaysLabel: "Always", AlwaysHonored: &honored,
	})
	var raw any
	if err := json.Unmarshal([]byte(card.JSON()), &raw); err != nil {
		t.Fatal(err)
	}
	actions := 0
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["a"] == "ix" {
				actions++
				if v["created_at"] != at.Format(time.RFC3339Nano) {
					t.Errorf("interaction action lacks its occurrence: %+v", v)
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(raw)
	if actions < 4 {
		t.Fatalf("fixture found only %d interaction actions", actions)
	}
}

func TestChannelAnnouncementClearsOnlyDifferentOccurrenceEnd(t *testing.T) {
	ft := newFleetTestNoStore(t)
	first := proto.Interaction{ID: "ix_channel_occurrence_end", MachineID: "mac", SessionID: "s1",
		Kind: proto.IxPermission, CreatedAt: time.Now().UTC()}
	ft.s.InteractionEnded(context.Background(), InteractionEnded{Interaction: first})
	if !ft.s.interactionEnded(first.ID) {
		t.Fatal("the first occurrence did not record its terminal marker")
	}
	replacement := first
	replacement.CreatedAt = first.CreatedAt.Add(time.Second)
	ft.s.InteractionAnnounced(context.Background(), InteractionAnnounced{Interaction: replacement})
	if ft.s.interactionEnded(first.ID) {
		t.Fatal("the earlier occurrence's terminal marker blocked its replacement")
	}
	ft.s.InteractionEnded(context.Background(), InteractionEnded{Interaction: first})
	ft.s.InteractionAnnounced(context.Background(), InteractionAnnounced{Interaction: first})
	if !ft.s.interactionEnded(first.ID) {
		t.Fatal("a duplicate announcement erased the same occurrence's in-flight terminal guard")
	}
}

func TestBotRestoreCardDoesNotOverwriteReplacement(t *testing.T) {
	ft := newFleetTestNoStore(t)
	preparedTestConversation(ft)
	first := proto.Interaction{ID: "ix_bot_restore_occurrence", MachineID: "mac", SessionID: "s1",
		Kind: proto.IxPermission, CreatedAt: time.Now().UTC()}
	replacement := first
	replacement.CreatedAt = first.CreatedAt.Add(time.Second)
	startPreparedInteraction(t, ft, replacement, approvalops.Operation{})
	ft.s.fbot.mu.Lock()
	ft.s.loadBotLocked(context.Background())
	if ft.s.fbot.cards == nil {
		ft.s.fbot.cards = map[string]botCard{}
	}
	ft.s.fbot.cards[first.ID] = botCard{MessageID: "om_replacement", Ix: replacement}
	ft.s.fbot.mu.Unlock()
	if ft.s.botRestoreCard(context.Background(), first.ID, botCard{MessageID: "om_first", Ix: first}) {
		t.Fatal("the earlier occurrence restored over a pending replacement")
	}
	ft.s.fbot.mu.Lock()
	got := ft.s.fbot.cards[first.ID]
	ft.s.fbot.mu.Unlock()
	if got.MessageID != "om_replacement" || !got.Ix.CreatedAt.Equal(replacement.CreatedAt) {
		t.Fatalf("the earlier occurrence replaced the bot-card ledger: %+v", got)
	}
}

func TestEarlierOccurrenceCannotDeleteReplacementBotCard(t *testing.T) {
	ft := newFleetTestNoStore(t)
	first := proto.Interaction{ID: "ix_bot_ledger_occurrence", MachineID: "mac", SessionID: "s1",
		Kind: proto.IxPermission, CreatedAt: time.Now().UTC()}
	replacement := first
	replacement.CreatedAt = first.CreatedAt.Add(time.Second)
	ft.s.fbot.mu.Lock()
	ft.s.loadBotLocked(context.Background())
	ft.s.keepBotCardLocked(first.ID, &botCard{MessageID: "om_replacement", Ix: replacement})
	ft.s.fbot.mu.Unlock()
	if _, _, same := ft.s.takeBotCardOccurrence(first.ID, first); same {
		t.Fatal("an earlier occurrence removed the replacement ledger entry")
	}
	ft.s.feishuInteractionEnded(first, "ended", false)
	ft.s.fbot.mu.Lock()
	got := ft.s.fbot.cards[first.ID]
	ft.s.fbot.mu.Unlock()
	if got.MessageID != "om_replacement" || !got.Ix.CreatedAt.Equal(replacement.CreatedAt) {
		t.Fatalf("an earlier terminal event damaged the replacement ledger: %+v", got)
	}
}

func TestFeishuStaleCallbackCannotActOnReplacement(t *testing.T) {
	for _, action := range []string{"resolve", "clarify"} {
		for _, swapAfterGet := range []bool{false, true} {
			name := action
			if swapAfterGet {
				name += "/swap_after_get"
			} else {
				name += "/visible_replacement"
			}
			t.Run(name, func(t *testing.T) {
				ft := newFleetTestNoStore(t)
				preparedTestConversation(ft)
				first := proto.Interaction{ID: "ix_feishu_stale_callback", MachineID: "mac", SessionID: "s1",
					Kind: proto.IxPermission, CanClarify: true, CreatedAt: time.Now().UTC()}
				firstDone := startPreparedInteraction(t, ft, first, approvalops.Operation{})
				ft.s.fleet.mu.Lock()
				p := ft.s.fleet.interactions[first.ID]
				ft.s.fleet.mu.Unlock()

				replacement := first
				replacement.CreatedAt = first.CreatedAt.Add(time.Second)
				var applies atomic.Int32
				replacementDone := make(chan preparedTestResult, 1)
				var replacementStarted atomic.Bool
				startReplacement := func() {
					if !replacementStarted.CompareAndSwap(false, true) {
						return
					}
					go func() {
						out, err := ft.s.waitPreparedCard(context.Background(), replacement, approvalops.Operation{Apply: func(context.Context, proto.Resolution) (string, error) {
							applies.Add(1)
							return "replacement applied", nil
						}})
						replacementDone <- preparedTestResult{out: out, err: err}
					}()
				}
				t.Cleanup(func() {
					if !replacementStarted.Load() {
						return
					}
					ft.s.fleet.mu.Lock()
					current := ft.s.fleet.interactions[first.ID]
					ft.s.fleet.mu.Unlock()
					if current != nil {
						ft.s.endPrepared(current, "cancelled", "test cleanup")
					}
				})
				currentIsReplacement := func() bool {
					ft.s.fleet.mu.Lock()
					defer ft.s.fleet.mu.Unlock()
					current := ft.s.fleet.interactions[first.ID]
					return current != nil && current.ix.CreatedAt.Equal(replacement.CreatedAt)
				}
				if !swapAfterGet {
					ft.s.endPrepared(p, "cancelled", "test replacement")
					select {
					case <-firstDone:
					case <-time.After(5 * time.Second):
						t.Fatal("the first occurrence did not finish")
					}
					startReplacement()
					waitFor(t, "the visible replacement", currentIsReplacement)
				}

				var ownerActions atomic.Int32
				var swapped atomic.Bool
				ft.f.answerAppBlocking(AppInteractions, assistantwire.InteractionsOwnerAction, func(json.RawMessage) (any, error) {
					ownerActions.Add(1)
					if swapAfterGet {
						ft.s.endPrepared(p, "cancelled", "test replacement after get")
						startReplacement()
						deadline := time.Now().Add(5 * time.Second)
						for !currentIsReplacement() && time.Now().Before(deadline) {
							time.Sleep(time.Millisecond)
						}
						if currentIsReplacement() {
							swapped.Store(true)
							// This is the replacement announcement's ledger write. It
							// must survive the stale press's later cleanup/restore.
							ft.s.fbot.mu.Lock()
							ft.s.fbot.cards[first.ID] = botCard{MessageID: "om_replacement", Ix: replacement}
							ft.s.fbot.mu.Unlock()
						}
					}
					return map[string]any{}, nil
				})
				ft.s.fbot.mu.Lock()
				ft.s.loadBotLocked(context.Background())
				if ft.s.fbot.cards == nil {
					ft.s.fbot.cards = map[string]botCard{}
				}
				ledger := replacement
				messageID := "om_replacement"
				if swapAfterGet {
					ledger = first
					messageID = "om_first"
				}
				ft.s.fbot.cards[first.ID] = botCard{MessageID: messageID, Ix: ledger}
				ft.s.fbot.mu.Unlock()
				var a feishubot.CardAction
				a.Action.Value = map[string]any{"a": "ix", "id": first.ID, "created_at": first.CreatedAt.Format(time.RFC3339Nano)}
				if action == "resolve" {
					a.Action.Value["allow"] = "1"
				} else {
					a.Action.Value["clarify"] = "1"
					a.Action.FormValue = map[string]any{"question": "stale question"}
				}
				a.Context.ChatID, a.Context.MessageID = "oc_owner", "om_card"
				resp := ft.s.botCardAction(context.Background(), &a, "stale-owner-action")
				if swapAfterGet {
					waitFor(t, "the real replacement at the owner-action boundary", swapped.Load)
				}
				if resp != nil && resp.Toast != nil && resp.Toast.Type == "success" {
					t.Fatalf("stale %s succeeded on the replacement: %+v", action, resp)
				}
				wantOwnerActions := int32(0)
				if swapAfterGet {
					// The old occurrence's action can be accepted before the
					// replacement; the final occurrence claim must still reject it.
					wantOwnerActions = 1
					select {
					case <-firstDone:
					case <-time.After(5 * time.Second):
						t.Fatal("the swapped first occurrence did not finish")
					}
				}
				if got := ownerActions.Load(); got != wantOwnerActions {
					t.Fatalf("stale %s consumed %d owner actions, want %d", action, got, wantOwnerActions)
				}
				if got := applies.Load(); got != 0 {
					t.Fatalf("stale resolve applied the replacement %d times", got)
				}
				if got := len(ownerQuestionSends(ft)); got != 0 {
					t.Fatalf("stale clarification sent %d questions to the replacement conversation", got)
				}
				ft.s.fbot.mu.Lock()
				gotLedger := ft.s.fbot.cards[first.ID]
				ft.s.fbot.mu.Unlock()
				if gotLedger.MessageID != "om_replacement" || !gotLedger.Ix.CreatedAt.Equal(replacement.CreatedAt) {
					t.Fatalf("stale %s damaged the replacement bot-card ledger: %+v", action, gotLedger)
				}
				ft.waitPending(1)
			})
		}
	}
}
