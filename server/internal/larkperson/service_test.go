package larkperson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

var testNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

type fakeSettings struct {
	mu           sync.Mutex
	values       map[string]json.RawMessage
	failOn       string
	failRevision int64
	failCompare  error
}

func newFakeSettings() *fakeSettings { return &fakeSettings{values: map[string]json.RawMessage{}} }

func (s *fakeSettings) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (s *fakeSettings) PutSetting(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.values[key] = raw
	s.mu.Unlock()
	return nil
}

func (s *fakeSettings) CompareSetting(_ context.Context, key string, revision int64, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var next struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(raw, &next); err != nil {
		return false, err
	}
	if s.failCompare != nil && strings.Contains(key, s.failOn) && (s.failRevision == 0 || s.failRevision == next.Revision) {
		return false, s.failCompare
	}
	current, exists := s.values[key]
	var got int64
	if exists {
		var envelope struct {
			Revision int64 `json:"revision"`
		}
		if err := json.Unmarshal(current, &envelope); err != nil {
			return false, err
		}
		got = envelope.Revision
	}
	if got != revision || revision == 0 && exists {
		return false, nil
	}
	s.values[key] = raw
	return true, nil
}

func (s *fakeSettings) SettingsPrefix(_ context.Context, prefix string) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]json.RawMessage{}
	for key, value := range s.values {
		if strings.HasPrefix(key, prefix) {
			out[key] = value
		}
	}
	return out, nil
}

func (s *fakeSettings) edit(t *testing.T, prefix string, edit func(map[string]any)) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, raw := range s.values {
		if !strings.Contains(key, prefix) {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		edit(value)
		updated, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		s.values[key] = updated
		return
	}
	t.Fatalf("no setting contains %q", prefix)
}

type sendCall struct {
	parent string
	text   string
	key    string
	thread bool
}

type fakeTransport struct {
	mu          sync.Mutex
	auth        adapter.AuthStatus
	people      []Person
	messages    []adapter.Message
	replyRaw    json.RawMessage
	replyErr    error
	listErr     error
	searchErr   error
	beforeReply func()
	calls       []sendCall
	rootCalls   int
	authCalls   atomic.Int64
	listCalls   atomic.Int64
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		auth:     adapter.AuthStatus{Brand: "feishu", AppID: "cli_bot", OpenID: "ou_owner", LoggedIn: true},
		replyRaw: json.RawMessage(`{"ok":true,"data":{"message_id":"om_request"}}`),
	}
}

func (f *fakeTransport) AuthStatus(context.Context) (adapter.AuthStatus, error) {
	f.authCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth, nil
}

func (f *fakeTransport) SearchPeople(context.Context, string, int) ([]Person, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Person(nil), f.people...), f.searchErr
}

func (f *fakeTransport) ListMessages(context.Context, string, string, string, int) (adapter.MessagePage, error) {
	f.listCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.listErr
	if len(f.calls) == 0 {
		err = nil
	}
	return adapter.MessagePage{Messages: append([]adapter.Message(nil), f.messages...)}, err
}

func (f *fakeTransport) SendText(context.Context, string, string, string) (json.RawMessage, error) {
	f.mu.Lock()
	f.rootCalls++
	f.mu.Unlock()
	return nil, errors.New("root send must not be called")
}

func (f *fakeTransport) ReplyText(_ context.Context, parent, text, key string, inThread bool) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sendCall{parent: parent, text: text, key: key, thread: inThread})
	before := f.beforeReply
	raw, err := f.replyRaw, f.replyErr
	f.mu.Unlock()
	if before != nil {
		before()
	}
	return raw, err
}

func (f *fakeTransport) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeReplyReader struct {
	mu          sync.Mutex
	observation connectorwire.LarkReplyObservation
	err         error
	calls       int
}

func (f *fakeReplyReader) ObserveReply(context.Context, connectorwire.LarkReplyWatch) (connectorwire.LarkReplyObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.observation, f.err
}

type fakeRecorder struct {
	mu       sync.Mutex
	calls    int
	taskID   string
	parkID   string
	observed connectorwire.LarkReplyObservation
	matched  bool
}

func (f *fakeRecorder) RecordLarkPersonReply(_ context.Context, taskID, parkID string, observation connectorwire.LarkReplyObservation) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.taskID, f.parkID, f.observed = taskID, parkID, observation
	return f.matched, nil
}

func endpointFixture() Endpoint {
	return Endpoint{
		ID: "endpoint_one", Kind: Kind, DisplayName: "Synthetic collaborator", Source: Source,
		Brand: "feishu", AppID: "cli_bot", AccountOpenID: "ou_owner", PersonOpenID: "ou_person",
		PersonVerifiedAt: testNow.Add(-time.Hour), AllowedChatIDs: []string{"oc_allowed"}, Enabled: true,
	}
}

func requestFixture() DeliveryRequest {
	return DeliveryRequest{
		ClientID: "client_one", TaskID: "todo_one", ParkInstance: "park_one", EndpointID: "endpoint_one",
		EndpointRevision: 2, Payload: "Synthetic task payload", ChatID: "oc_allowed", ThreadID: "omt_task",
		ParentMessageID: "om_parent",
	}
}

func parentFixture() adapter.Message {
	return adapter.Message{MessageID: "om_parent", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text",
		CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339)}
}

func sentFixture() adapter.Message {
	return adapter.Message{MessageID: "om_request", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text",
		CreateTime:     testNow.Add(-2 * time.Second).Format(time.RFC3339),
		MessageAppLink: "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_allowed&open_thread_id=omt_task&message_id=om_request",
		Sender:         adapter.Sender{ID: "cli_bot", IDType: "app_id", SenderType: "app", AppID: "cli_bot"}}
}

func serviceFixture(t *testing.T) (*Service, *fakeSettings, *fakeTransport, *fakeReplyReader) {
	t.Helper()
	settings := newFakeSettings()
	transport := newFakeTransport()
	transport.messages = []adapter.Message{parentFixture(), sentFixture()}
	reader := &fakeReplyReader{}
	svc, err := New(settings, transport, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	return svc, settings, transport, reader
}

func createEndpoint(t *testing.T, svc *Service) Endpoint {
	t.Helper()
	endpoint := endpointFixture()
	created, err := svc.CreateEndpoint(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.PersonOpenID != "" || !created.PersonVerifiedAt.IsZero() {
		t.Fatalf("create trusted client identity or wrong revision: %+v", created)
	}
	verified, err := svc.VerifyPerson(context.Background(), created.ID, "Synthetic collaborator", "ou_person")
	if err == nil {
		t.Fatal("directory verification unexpectedly succeeded without a directory result")
	}
	return verified
}

func verifiedService(t *testing.T) (*Service, *fakeSettings, *fakeTransport, *fakeReplyReader) {
	t.Helper()
	svc, settings, transport, reader := serviceFixture(t)
	transport.people = []Person{{OpenID: "ou_person", Name: "Synthetic collaborator"}}
	endpoint := endpointFixture()
	endpoint.Revision = 0
	created, err := svc.CreateEndpoint(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if created.PersonOpenID != "" || !created.PersonVerifiedAt.IsZero() {
		t.Fatalf("client supplied identity was trusted: %+v", created)
	}
	if _, err := svc.VerifyPerson(context.Background(), created.ID, "Synthetic collaborator", "ou_person"); err != nil {
		t.Fatal(err)
	}
	return svc, settings, transport, reader
}

func TestEndpointIdentityResolutionIsServerSideAndUnique(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	got, err := svc.Endpoint(context.Background(), "endpoint_one")
	if err != nil || got.PersonOpenID != "ou_person" || got.PersonVerifiedAt.IsZero() {
		t.Fatalf("verified endpoint=%+v err=%v", got, err)
	}
	listed, err := svc.Endpoints(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != got.ID || listed[0].Revision != got.Revision {
		t.Fatalf("listed endpoints=%+v err=%v", listed, err)
	}
	transport.people = []Person{{OpenID: "ou_person", Name: "Synthetic collaborator"}, {OpenID: "ou_other", Name: "Synthetic collaborator"}}
	if _, err := svc.VerifyPerson(context.Background(), got.ID, got.DisplayName, "ou_person"); !errors.Is(err, ErrAmbiguousPerson) {
		t.Fatalf("ambiguous directory result err=%v", err)
	}
	transport.people = []Person{{OpenID: "ou_other", Name: "Synthetic collaborator"}}
	if _, err := svc.VerifyPerson(context.Background(), got.ID, got.DisplayName, "ou_person"); !errors.Is(err, ErrAmbiguousPerson) {
		t.Fatalf("selected OpenID absent err=%v", err)
	}
}

func TestDeliveryReserveSendReadbackAndBinding(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	got, err := svc.Deliver(context.Background(), requestFixture())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateSent || got.RequestMessageID != "om_request" || got.PayloadHash == "" || got.IdempotencyKey == "" {
		t.Fatalf("delivery=%+v", got)
	}
	if transport.sendCount() != 1 || transport.rootCalls != 0 {
		t.Fatalf("send calls=%d root=%d", transport.sendCount(), transport.rootCalls)
	}
	call := transport.calls[0]
	if call.parent != "om_parent" || call.text != requestFixture().Payload || !call.thread || call.key != got.IdempotencyKey {
		t.Fatalf("transport call=%+v", call)
	}
	binding, err := svc.Binding(context.Background(), got.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Watch.PersonOpenID != "ou_person" || binding.Watch.AccountOpenID != "ou_owner" ||
		binding.Watch.AppID != "cli_bot" || binding.Watch.ChatID != "oc_allowed" || binding.Watch.ThreadID != "omt_task" ||
		binding.Watch.RequestMessageID != "om_request" || binding.SentAt.IsZero() || binding.URL == "" {
		t.Fatalf("binding=%+v", binding)
	}
	again, err := svc.Deliver(context.Background(), requestFixture())
	if err != nil || again.State != StateSent || transport.sendCount() != 1 {
		t.Fatalf("duplicate delivery=%+v err=%v calls=%d", again, err, transport.sendCount())
	}
}

func TestEndpointRevisionCannotRedirectApprovedDelivery(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	original := requestFixture()
	transport.beforeReply = func() {
		current, err := svc.Endpoint(context.Background(), original.EndpointID)
		if err != nil {
			t.Error(err)
			return
		}
		current.DisplayName = "Edited display only"
		current.AllowedChatIDs = []string{"oc_redirected"}
		if _, err := svc.UpdateEndpoint(context.Background(), current); err != nil {
			t.Error(err)
		}
	}
	first, err := svc.Deliver(context.Background(), original)
	if err != nil || first.State != StateSent || transport.calls[0].parent != "om_parent" {
		t.Fatalf("frozen first delivery=%+v err=%v calls=%+v", first, err, transport.calls)
	}
	redirected := original
	redirected.EndpointRevision = 3
	redirected.ChatID = "oc_redirected"
	redirected.ThreadID = "omt_redirected"
	redirected.ParentMessageID = "om_redirected"
	if _, err := svc.Deliver(context.Background(), redirected); !errors.Is(err, ErrConflict) {
		t.Fatalf("redirected same client ID err=%v", err)
	}
	if transport.sendCount() != 1 {
		t.Fatalf("redirect caused another send: %d", transport.sendCount())
	}
}

func TestPreSendReservationAndClaimFailuresNeverSend(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failOn       string
		failRevision int64
	}{
		{name: "initial reservation", failOn: ".delivery.", failRevision: 1},
		{name: "topic claim", failOn: ".topic.", failRevision: 1},
		{name: "sending transition", failOn: ".delivery.", failRevision: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, settings, transport, _ := verifiedService(t)
			settings.failOn = tc.failOn
			settings.failRevision = tc.failRevision
			settings.failCompare = errors.New("synthetic storage failure with oc_secret and secret payload")
			transport.authCalls.Store(0)
			transport.listCalls.Store(0)
			_, err := svc.Deliver(context.Background(), requestFixture())
			if err == nil || transport.sendCount() != 0 || transport.authCalls.Load() != 0 || transport.listCalls.Load() != 0 {
				t.Fatalf("err=%v sends=%d auth=%d reads=%d", err, transport.sendCount(), transport.authCalls.Load(), transport.listCalls.Load())
			}
			if strings.Contains(err.Error(), "oc_secret") || strings.Contains(err.Error(), "secret payload") {
				t.Fatalf("error leaked private fixture: %v", err)
			}
		})
	}
}

func TestSameClientIDDifferentPayloadRejected(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	changed := requestFixture()
	changed.Payload += " changed"
	if _, err := svc.Deliver(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload conflict err=%v", err)
	}
	if transport.sendCount() != 1 {
		t.Fatalf("calls=%d", transport.sendCount())
	}
}

func TestLostResponseAndReadbackAreUnknownWithoutRepost(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replyErr  error
		listErr   error
		emptyBody bool
	}{
		{name: "lost response", replyErr: &adapter.Error{Kind: adapter.KindTimeout}},
		{name: "lost readback", listErr: errors.New("synthetic read failure")},
		{name: "empty response", emptyBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, settings, transport, reader := verifiedService(t)
			transport.replyErr, transport.listErr = tc.replyErr, tc.listErr
			if tc.emptyBody {
				transport.replyRaw = nil
			}
			got, err := svc.Deliver(context.Background(), requestFixture())
			if !errors.Is(err, ErrUnknown) || got.State != StateUnknown {
				t.Fatalf("delivery=%+v err=%v", got, err)
			}
			restarted, err := New(settings, transport, reader, func() time.Time { return testNow })
			if err != nil {
				t.Fatal(err)
			}
			again, err := restarted.Deliver(context.Background(), requestFixture())
			if !errors.Is(err, ErrUnknown) || again.State != StateUnknown || transport.sendCount() != 1 {
				t.Fatalf("retry=%+v err=%v calls=%d", again, err, transport.sendCount())
			}
		})
	}
}

func TestPreSendIdentityRecheckFailsWithoutTransportSend(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*adapter.AuthStatus)
	}{
		{name: "wrong brand", edit: func(a *adapter.AuthStatus) { a.Brand = "lark" }},
		{name: "wrong app", edit: func(a *adapter.AuthStatus) { a.AppID = "cli_other" }},
		{name: "wrong account", edit: func(a *adapter.AuthStatus) { a.OpenID = "ou_other" }},
		{name: "expired", edit: func(a *adapter.AuthStatus) { a.Expired = true }},
		{name: "logged out", edit: func(a *adapter.AuthStatus) { a.LoggedIn = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, transport, _ := verifiedService(t)
			tc.edit(&transport.auth)
			got, err := svc.Deliver(context.Background(), requestFixture())
			if err == nil || got.State != StateFailed || transport.sendCount() != 0 || transport.listCalls.Load() != 0 {
				t.Fatalf("delivery=%+v err=%v sends=%d reads=%d", got, err, transport.sendCount(), transport.listCalls.Load())
			}
		})
	}
}

func TestReadbackIdentityAndTimeMismatchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*fakeTransport)
	}{
		{name: "wrong bot", edit: func(f *fakeTransport) {
			f.messages[1].Sender.ID = "cli_other"
			f.messages[1].Sender.AppID = "cli_other"
		}},
		{name: "owner instead of bot", edit: func(f *fakeTransport) {
			f.messages[1].Sender = adapter.Sender{ID: "ou_owner", IDType: "open_id", SenderType: "user"}
		}},
		{name: "wrong person", edit: func(f *fakeTransport) {
			f.messages[1].Sender = adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}
		}},
		{name: "wrong chat", edit: func(f *fakeTransport) { f.messages[1].ChatID = "oc_other" }},
		{name: "wrong topic", edit: func(f *fakeTransport) { f.messages[1].ThreadID = "omt_other" }},
		{name: "wrong source link", edit: func(f *fakeTransport) { f.messages[1].MessageAppLink = "https://example.com/not-lark" }},
		{name: "old message", edit: func(f *fakeTransport) { f.messages[1].CreateTime = testNow.Add(-time.Hour).Format(time.RFC3339) }},
		{name: "future message", edit: func(f *fakeTransport) { f.messages[1].CreateTime = testNow.Add(time.Hour).Format(time.RFC3339) }},
		{name: "account changed", edit: func(f *fakeTransport) { f.beforeReply = func() { f.auth.OpenID = "ou_other" } }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, transport, _ := verifiedService(t)
			tc.edit(transport)
			got, err := svc.Deliver(context.Background(), requestFixture())
			if !errors.Is(err, ErrUnknown) || got.State != StateUnknown || transport.sendCount() != 1 {
				t.Fatalf("delivery=%+v err=%v calls=%d", got, err, transport.sendCount())
			}
			if _, err := svc.Deliver(context.Background(), requestFixture()); !errors.Is(err, ErrUnknown) || transport.sendCount() != 1 {
				t.Fatalf("mismatch reposted: err=%v calls=%d", err, transport.sendCount())
			}
		})
	}
}

func TestKnownNonDeliveryRetryUsesFrozenOperationAndKey(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	transport.replyErr = &adapter.Error{Kind: adapter.KindMissingPermission}
	first, err := svc.Deliver(context.Background(), requestFixture())
	if err == nil || first.State != StateFailed || first.IdempotencyKey == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	transport.replyErr = nil
	second, err := svc.Deliver(context.Background(), requestFixture())
	if err != nil || second.State != StateSent || transport.sendCount() != 2 {
		t.Fatalf("second=%+v err=%v calls=%d", second, err, transport.sendCount())
	}
	if transport.calls[0].key != transport.calls[1].key || transport.calls[0].parent != transport.calls[1].parent ||
		transport.calls[0].text != transport.calls[1].text || second.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("retry operation changed: first=%+v second=%+v calls=%+v", first, second, transport.calls)
	}
}

func TestConcurrentDeliveryAndRestartDoNotDuplicate(t *testing.T) {
	_, settings, transport, reader := verifiedService(t)
	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			competing, err := New(settings, transport, reader, func() time.Time { return testNow })
			if err != nil {
				errs <- err
				return
			}
			got, err := competing.Deliver(context.Background(), requestFixture())
			if err == nil && got.State != StateSent {
				err = fmt.Errorf("unexpected state %s", got.State)
			}
			if err != nil && !errors.Is(err, ErrUnknown) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if transport.sendCount() != 1 {
		t.Fatalf("concurrent sends=%d", transport.sendCount())
	}
	restarted, err := New(settings, transport, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Deliver(context.Background(), requestFixture())
	if err != nil && !errors.Is(err, ErrUnknown) || got.State != StateSent && got.State != StateUnknown || transport.sendCount() != 1 {
		t.Fatalf("restart delivery=%+v err=%v calls=%d", got, err, transport.sendCount())
	}
}

func TestRestartDuringSendingBecomesUnknownAndNeverReposts(t *testing.T) {
	svc, settings, transport, reader := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	settings.edit(t, ".delivery.", func(value map[string]any) {
		value["state"] = string(StateSending)
		value["request_message_id"] = ""
	})
	restarted, err := New(settings, transport, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Deliver(context.Background(), requestFixture())
	if !errors.Is(err, ErrUnknown) || got.State != StateUnknown || transport.sendCount() != 1 {
		t.Fatalf("delivery=%+v err=%v calls=%d", got, err, transport.sendCount())
	}
}

func TestRootDeliveryRejectedWithoutSend(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	req := requestFixture()
	req.ThreadID = ""
	if _, err := svc.Deliver(context.Background(), req); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("root delivery err=%v", err)
	}
	if transport.sendCount() != 0 || transport.rootCalls != 0 {
		t.Fatalf("root path sent: replies=%d roots=%d", transport.sendCount(), transport.rootCalls)
	}
}

func observationFixture(t *testing.T, svc *Service, clientID string) connectorwire.LarkReplyObservation {
	t.Helper()
	binding, err := svc.Binding(context.Background(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	return connectorwire.LarkReplyObservation{
		Watch: binding.Watch,
		Request: connectorwire.LarkReplyMessage{ID: binding.Watch.RequestMessageID, SenderID: "cli_bot", SenderType: "app", IDType: "app_id", AppID: "cli_bot",
			ChatID: "oc_allowed", ThreadID: "omt_task", URL: binding.URL, At: binding.SentAt, Before: binding.SentAt.Add(time.Second)},
		Reply: connectorwire.LarkReplyMessage{ID: "om_reply", SenderID: "ou_person", SenderType: "user", IDType: "open_id",
			ChatID: "oc_allowed", ThreadID: "omt_task", At: testNow.Add(-500 * time.Millisecond), Before: testNow.Add(-400 * time.Millisecond)},
		CheckedAt: testNow,
	}
}

func TestExactReplyCorrelationAndDuplicateReceipt(t *testing.T) {
	svc, settings, transport, reader := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	reader.observation = observationFixture(t, svc, "client_one")
	recorder := &fakeRecorder{matched: true}
	observation, err := svc.CorrelateReply(context.Background(), "client_one", recorder)
	if err != nil || observation.Reply.ID != "om_reply" || recorder.calls != 1 || recorder.taskID != "todo_one" || recorder.parkID != "park_one" {
		t.Fatalf("observation=%+v err=%v recorder=%+v", observation, err, recorder)
	}
	if _, err := svc.CorrelateReply(context.Background(), "client_one", recorder); err != nil || recorder.calls != 1 {
		t.Fatalf("duplicate correlation err=%v calls=%d", err, recorder.calls)
	}
	restarted, err := New(settings, transport, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CorrelateReply(context.Background(), "client_one", recorder); err != nil || recorder.calls != 1 {
		t.Fatalf("restart correlation err=%v calls=%d", err, recorder.calls)
	}
}

func TestWrongReplyPersonBotOwnerSourceAndTopicNeverReachPark(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*connectorwire.LarkReplyObservation)
	}{
		{name: "wrong person", edit: func(o *connectorwire.LarkReplyObservation) { o.Reply.SenderID = "ou_other" }},
		{name: "owner reply", edit: func(o *connectorwire.LarkReplyObservation) { o.Reply.SenderID = "ou_owner" }},
		{name: "bot reply", edit: func(o *connectorwire.LarkReplyObservation) {
			o.Reply.SenderType = "app"
			o.Reply.IDType = "app_id"
			o.Reply.AppID = "cli_bot"
		}},
		{name: "wrong request bot", edit: func(o *connectorwire.LarkReplyObservation) {
			o.Request.SenderID = "cli_other"
			o.Request.AppID = "cli_other"
		}},
		{name: "owner request", edit: func(o *connectorwire.LarkReplyObservation) {
			o.Request.SenderID = "ou_owner"
			o.Request.SenderType = "user"
			o.Request.IDType = "open_id"
			o.Request.AppID = ""
		}},
		{name: "wrong source", edit: func(o *connectorwire.LarkReplyObservation) { o.Watch.Source = "other" }},
		{name: "wrong chat", edit: func(o *connectorwire.LarkReplyObservation) { o.Reply.ChatID = "oc_other" }},
		{name: "wrong topic", edit: func(o *connectorwire.LarkReplyObservation) { o.Reply.ThreadID = "omt_other" }},
		{name: "duplicate request message", edit: func(o *connectorwire.LarkReplyObservation) { o.Reply.ID = o.Request.ID }},
		{name: "reply before request", edit: func(o *connectorwire.LarkReplyObservation) {
			o.Reply.At = o.Request.At.Add(-time.Second)
			o.Reply.Before = o.Request.At
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, reader := verifiedService(t)
			if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
				t.Fatal(err)
			}
			observation := observationFixture(t, svc, "client_one")
			tc.edit(&observation)
			reader.observation = observation
			recorder := &fakeRecorder{matched: true}
			if _, err := svc.CorrelateReply(context.Background(), "client_one", recorder); err == nil || recorder.calls != 0 {
				t.Fatalf("err=%v recorder calls=%d", err, recorder.calls)
			}
		})
	}
}

func TestProactiveInboxIsScopedFYIWithoutPrivilegedEffects(t *testing.T) {
	svc, settings, transport, reader := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	transport.messages = []adapter.Message{
		{MessageID: "om_proactive", ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Content: json.RawMessage(`{"text":"private body"}`), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_proactive", ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_other_person", ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_other", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_owner", ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_owner", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_bot", ChatID: "oc_allowed", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "cli_bot", IDType: "app_id", SenderType: "app", AppID: "cli_bot"}},
		{MessageID: "om_wrong_chat", ChatID: "oc_other", ThreadID: "omt_proactive", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_wrong_topic", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}},
	}
	read := ProactiveRequest{EndpointID: "endpoint_one", EndpointRevision: 2, ChatID: "oc_allowed", ThreadID: "omt_proactive", EventID: "event_one"}
	things, err := svc.ReadProactive(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	if len(things) != 1 {
		t.Fatalf("things=%+v", things)
	}
	thing := things[0]
	if thing.Tier != connectorwire.TierFYI || thing.Last != "om_proactive" || thing.Parent != nil || thing.Preview != "" || thing.Context != "" || thing.CanReply {
		t.Fatalf("unsafe proactive thing=%+v", thing)
	}
	if strings.Contains(thing.Thread, "oc_allowed") || strings.Contains(thing.Thread, "omt_proactive") || strings.Contains(fmt.Sprint(thing), "private body") {
		t.Fatalf("thing exposed private source identifiers/body: %+v", thing)
	}
	if transport.sendCount() != 1 || transport.rootCalls != 0 || reader.calls != 0 {
		t.Fatalf("proactive read caused writes/correlation: sends=%d roots=%d replies=%d", transport.sendCount(), transport.rootCalls, reader.calls)
	}
	again, err := svc.ReadProactive(context.Background(), read)
	if err != nil || len(again) != 1 || again[0].Thread != thing.Thread || again[0].Last != thing.Last {
		t.Fatalf("duplicate read=%+v err=%v", again, err)
	}
	restarted, err := New(settings, transport, reader, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	afterRestart, err := restarted.ReadProactive(context.Background(), read)
	if err != nil || len(afterRestart) != 1 || afterRestart[0].Thread != thing.Thread || afterRestart[0].Last != thing.Last {
		t.Fatalf("restart read=%+v err=%v", afterRestart, err)
	}
	read.ChatID = "oc_not_allowed"
	if _, err := svc.ReadProactive(context.Background(), read); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unallowlisted chat err=%v", err)
	}
	current, err := svc.Endpoint(context.Background(), read.EndpointID)
	if err != nil {
		t.Fatal(err)
	}
	current.AllowedChatIDs = nil
	updated, err := svc.UpdateEndpoint(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	read.EndpointRevision = updated.Revision
	read.ChatID = "oc_allowed"
	if _, err := svc.ReadProactive(context.Background(), read); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty proactive allowlist err=%v", err)
	}
}

func TestProactiveExactTaskParentAndEventConflict(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	transport.messages = []adapter.Message{{MessageID: "om_task_reply", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text",
		CreateTime: testNow.Add(-time.Minute).Format(time.RFC3339), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}}}
	read := ProactiveRequest{EndpointID: "endpoint_one", EndpointRevision: 2, ChatID: "oc_allowed", ThreadID: "omt_task", EventID: "event_task"}
	things, err := svc.ReadProactive(context.Background(), read)
	if err != nil || len(things) != 1 || things[0].Parent == nil || things[0].Parent.Kind != "todo" || things[0].Parent.ID != "todo_one" {
		t.Fatalf("things=%+v err=%v", things, err)
	}
	transport.messages[0].MessageID = "om_changed_for_same_event"
	if _, err := svc.ReadProactive(context.Background(), read); !errors.Is(err, ErrConflict) {
		t.Fatalf("event reuse err=%v", err)
	}
}

func TestOneActiveTaskPerExactTopic(t *testing.T) {
	svc, _, transport, _ := verifiedService(t)
	if _, err := svc.Deliver(context.Background(), requestFixture()); err != nil {
		t.Fatal(err)
	}
	second := requestFixture()
	second.ClientID = "client_two"
	second.Payload = "Another synthetic task"
	if _, err := svc.Deliver(context.Background(), second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second topic task err=%v", err)
	}
	if transport.sendCount() != 1 {
		t.Fatalf("calls=%d", transport.sendCount())
	}
}
