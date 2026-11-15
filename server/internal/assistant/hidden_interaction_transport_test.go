package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// A saved visibility transition must update a client that stays connected, even
// when a previous manager version omitted the card from its reconnect snapshot.
func TestHiddenDirectoryPendingCardTransportTransitions(t *testing.T) {
	ft := newFleetTest(t)
	ft.d.setSessions("mac", []proto.Session{{SessionID: "blocked", Cwd: "/work/noisy", Status: proto.StatusBlocked}})
	ix := proto.Interaction{ID: "ix_hidden", MachineID: "mac", SessionID: "blocked", Kind: proto.IxPermission, Summary: "Approve work", CreatedAt: time.Now()}
	ft.s.fleet.mu.Lock()
	ft.s.fleet.interactions = map[string]*pending{ix.ID: {ix: ix, told: false}}
	ft.s.fleet.mu.Unlock()
	on := hiddenAgentDirs{Rules: []hiddenAgentDir{{MachineID: "mac", Pattern: "/work/noisy", Enabled: true}}}
	off := hiddenAgentDirs{Rules: []hiddenAgentDir{{MachineID: "mac", Pattern: "/work/noisy", Enabled: false}}}
	for _, step := range []struct {
		name  string
		rules hiddenAgentDirs
	}{{"add", on}, {"disable", off}, {"enable", on}, {"remove", hiddenAgentDirs{Rules: []hiddenAgentDir{}}}} {
		t.Run(step.name, func(t *testing.T) {
			ft.f.mu.Lock()
			ft.f.told = nil
			ft.f.mu.Unlock()
			if err := ft.s.saveHiddenDirs(context.Background(), step.rules, nil); err != nil {
				t.Fatal(err)
			}
			if len(ft.s.pendingInteractions()) != 1 {
				t.Fatal("authoritative inventory lost the pending card")
			}
			hidden := step.name == "add" || step.name == "enable"
			raw, handled, err := ft.s.localFleetDeviceCall(context.Background(), DeviceCall{Device: DeviceAll, Method: DeviceInteractionsList})
			if err != nil || !handled {
				t.Fatalf("projection: %v %v", handled, err)
			}
			var projection struct {
				Interactions []proto.Interaction `json:"interactions"`
			}
			if err := json.Unmarshal(raw, &projection); err != nil {
				t.Fatal(err)
			}
			if got := len(projection.Interactions); (got == 0) != hidden {
				t.Fatalf("visible projection: hidden=%v cards=%d", hidden, got)
			}
			ft.s.fleet.mu.Lock()
			told := ft.s.fleet.interactions[ix.ID].told
			ft.s.fleet.mu.Unlock()
			if told {
				t.Fatal("refresh changed delegation/told state")
			}
			if len(ft.f.pushed()) != 0 {
				t.Fatal("visibility refresh sent a notification")
			}
			ft.f.mu.Lock()
			defer ft.f.mu.Unlock()
			delivered, sessionsDelivered := false, false
			for _, event := range ft.f.told {
				if event.event == assistantwire.EventFleetSessions {
					sessionsDelivered = true
				}
				if event.event == assistantwire.EventFleetInteraction {
					if !sessionsDelivered {
						t.Fatal("card replay preceded the new session visibility")
					}
					var p assistantwire.FleetInteraction
					_ = json.Unmarshal(event.params, &p)
					delivered = delivered || p.Interaction.ID == ix.ID
				}
				if event.event == assistantwire.EventFleetResolved {
					t.Fatal("visibility resolved a pending card")
				}
			}
			if !delivered {
				t.Fatal("pending card was not delivered to an already-connected client")
			}
		})
	}
}
