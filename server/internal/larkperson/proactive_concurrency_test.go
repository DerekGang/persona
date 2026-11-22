package larkperson

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func proactiveMessage(id string) adapter.Message {
	return adapter.Message{MessageID: id, ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text",
		CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339),
		Sender:     adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}}
}

func proactiveReadFixture() ProactiveRequest {
	return ProactiveRequest{EndpointID: "endpoint_one", EndpointRevision: 2, ChatID: "oc_allowed", ThreadID: "omt_proactive", EventID: "event_shared"}
}

func TestProactiveConcurrentDuplicateReadWritesOneReceipt(t *testing.T) {
	_, settings, transport, reader := verifiedService(t)
	transport.messages = []adapter.Message{proactiveMessage("om_duplicate")}
	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc, err := New(settings, transport, reader, func() time.Time { return testNow })
			if err != nil {
				errs <- err
				return
			}
			things, err := svc.ReadProactive(context.Background(), proactiveReadFixture())
			if err == nil && (len(things) != 1 || things[0].Last != "om_duplicate") {
				err = errors.New("unexpected proactive result")
			}
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	inbox, err := settings.SettingsPrefix(context.Background(), inboxPrefix)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox receipts=%d err=%v", len(inbox), err)
	}
	events, err := settings.SettingsPrefix(context.Background(), eventPrefix)
	if err != nil || len(events) != 1 {
		t.Fatalf("event receipts=%d err=%v", len(events), err)
	}
}

func TestProactiveConcurrentConflictingEventLeavesOneReceipt(t *testing.T) {
	first, settings, transportA, reader := verifiedService(t)
	transportA.messages = []adapter.Message{proactiveMessage("om_a")}
	transportB := newFakeTransport()
	transportB.messages = []adapter.Message{proactiveMessage("om_b")}
	second, err := New(settings, transportB, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, svc := range []*Service{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.ReadProactive(context.Background(), proactiveReadFixture())
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("succeeded=%d conflicts=%d", succeeded, conflicts)
	}
	inbox, err := settings.SettingsPrefix(context.Background(), inboxPrefix)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox receipts=%d err=%v", len(inbox), err)
	}
}
