package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/larkperson"
)

type integrationPersonSettings struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func newIntegrationPersonSettings() *integrationPersonSettings {
	return &integrationPersonSettings{values: map[string]json.RawMessage{}}
}

func (s *integrationPersonSettings) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (s *integrationPersonSettings) PutSetting(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.values[key] = raw
	s.mu.Unlock()
	return nil
}

func (s *integrationPersonSettings) CompareSetting(_ context.Context, key string, revision int64, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *integrationPersonSettings) SettingsPrefix(_ context.Context, prefix string) (map[string]json.RawMessage, error) {
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

type integrationPersonTransport struct {
	mu       sync.Mutex
	auth     adapter.AuthStatus
	people   []larkperson.Person
	messages []adapter.Message
	calls    int
	roots    int
	lastKey  string
}

func (f *integrationPersonTransport) AuthStatus(context.Context) (adapter.AuthStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth, nil
}

func (f *integrationPersonTransport) SearchPeople(context.Context, string, int) ([]larkperson.Person, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]larkperson.Person(nil), f.people...), nil
}

func (f *integrationPersonTransport) ListMessages(context.Context, string, string, string, int) (adapter.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return adapter.MessagePage{Messages: append([]adapter.Message(nil), f.messages...)}, nil
}

func (f *integrationPersonTransport) SendText(context.Context, string, string, string) (json.RawMessage, error) {
	f.mu.Lock()
	f.roots++
	f.mu.Unlock()
	return nil, errors.New("root send must not be called")
}

func (f *integrationPersonTransport) ReplyText(_ context.Context, _, _, key string, inThread bool) (json.RawMessage, error) {
	if !inThread {
		return nil, errors.New("topic reply required")
	}
	f.mu.Lock()
	f.calls++
	f.lastKey = key
	f.mu.Unlock()
	return json.RawMessage(`{"data":{"message_id":"om_request"}}`), nil
}

func (f *integrationPersonTransport) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.roots
}

type integrationPersonReader struct {
	observation connectorwire.LarkReplyObservation
}

func (f *integrationPersonReader) ObserveReply(context.Context, connectorwire.LarkReplyWatch) (connectorwire.LarkReplyObservation, error) {
	return f.observation, nil
}

func TestLarkPersonDeliveryReadbackReplyAndProactiveEndToEnd(t *testing.T) {
	assistantService, fakeHub, todo := newSupervisedService(t)
	holdDigest(assistantService)
	now := time.Now().UTC().Truncate(time.Second)
	parkAt := personReplyParkAt(now)
	deliveryNow := now.Add(-10 * time.Minute)
	clock := deliveryNow
	settings := newIntegrationPersonSettings()
	transport := &integrationPersonTransport{
		auth:   adapter.AuthStatus{Brand: "feishu", AppID: "cli_bot", OpenID: "ou_owner", LoggedIn: true},
		people: []larkperson.Person{{OpenID: "ou_person", Name: "Synthetic collaborator"}},
	}
	requestAt := deliveryNow.Add(-time.Second)
	topicURL := "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_allowed&open_thread_id=omt_task&message_id=om_request"
	transport.messages = []adapter.Message{
		{MessageID: "om_parent", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: now.Add(-20 * time.Minute).Format(time.RFC3339)},
		{MessageID: "om_request", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: requestAt.Format(time.RFC3339), MessageAppLink: topicURL,
			Sender: adapter.Sender{ID: "cli_bot", IDType: "app_id", SenderType: "app", AppID: "cli_bot"}},
	}
	reader := &integrationPersonReader{}
	larkService, err := larkperson.New(settings, transport, reader, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	created, err := larkService.CreateEndpoint(context.Background(), larkperson.Endpoint{
		ID: "endpoint_integration", Kind: larkperson.Kind, DisplayName: "Synthetic collaborator", Source: larkperson.Source,
		Brand: "feishu", AppID: "cli_bot", AccountOpenID: "ou_owner", AllowedChatIDs: []string{"oc_allowed"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := larkService.VerifyPerson(context.Background(), created.ID, created.DisplayName, "ou_person")
	if err != nil {
		t.Fatal(err)
	}
	request := larkperson.DeliveryRequest{ClientID: "client_integration", TaskID: todo.ID, ParkInstance: parkAt.Format(time.RFC3339Nano),
		EndpointID: endpoint.ID, EndpointRevision: endpoint.Revision, Payload: "Synthetic task payload", ChatID: "oc_allowed",
		ThreadID: "omt_task", ParentMessageID: "om_parent"}
	delivery, err := larkService.Deliver(context.Background(), request)
	if err != nil || delivery.State != larkperson.StateSent {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	binding, err := larkService.Binding(context.Background(), request.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	args := personReplyParkArgs(now)
	evidence := args["evidence"].(map[string]any)
	contact := evidence["contact"].(map[string]any)
	contact["url"] = binding.URL
	contact["sent_at"] = binding.SentAt.Format(time.RFC3339)
	watchRaw, err := json.Marshal(binding.Watch)
	if err != nil {
		t.Fatal(err)
	}
	var watch map[string]any
	if err := json.Unmarshal(watchRaw, &watch); err != nil {
		t.Fatal(err)
	}
	evidence["reply"] = watch
	if _, err := reviewErr(assistantService, todo.ID, args); err != nil {
		t.Fatalf("park with sent binding: %v", err)
	}
	if _, _, err := assistantService.updateSupervision(context.Background(), todo.ID, func(v *Supervision) bool {
		v.Reviews[len(v.Reviews)-1].At = parkAt
		return true
	}); err != nil {
		t.Fatal(err)
	}
	clock = now
	reader.observation = connectorwire.LarkReplyObservation{
		Watch: binding.Watch,
		Request: connectorwire.LarkReplyMessage{ID: "om_request", SenderID: "cli_bot", SenderType: "app", IDType: "app_id", AppID: "cli_bot",
			ChatID: "oc_allowed", ThreadID: "omt_task", URL: binding.URL, At: binding.SentAt, Before: binding.SentAt.Add(time.Second)},
		Reply: connectorwire.LarkReplyMessage{ID: "om_reply", SenderID: "ou_person", SenderType: "user", IDType: "open_id",
			ChatID: "oc_allowed", ThreadID: "omt_task", At: now.Add(-2 * time.Minute), Before: now.Add(-2*time.Minute + time.Second)},
		CheckedAt: now,
	}
	if _, err := larkService.CorrelateReply(context.Background(), request.ClientID, assistantService); err != nil {
		t.Fatalf("correlate exact reply: %v", err)
	}
	got := todoNow(assistantService, todo.ID)
	if got.Supervision.State != reviewPending || got.DoneAt != nil || len(got.Supervision.Reviews) == 0 ||
		got.Supervision.Reviews[len(got.Supervision.Reviews)-1].Reply == nil {
		t.Fatalf("exact reply did not persist pending review: %+v", got.Supervision)
	}
	if kinds := personReplyPendingKinds(assistantService, todo.ID); len(kinds) != 1 || kinds[0] != "review" {
		t.Fatalf("signals=%v", kinds)
	}
	if sends := sentToAgent(fakeHub); len(sends) != 0 {
		t.Fatalf("reply delivered to Agent: %v", sends)
	}
	if _, err := larkService.CorrelateReply(context.Background(), request.ClientID, assistantService); err != nil {
		t.Fatal(err)
	}
	if matched, err := assistantService.RecordLarkPersonReply(context.Background(), todo.ID, parkAt.Format(time.RFC3339Nano), reader.observation); err != nil || !matched {
		t.Fatalf("persisted receipt was not idempotent: matched=%v err=%v", matched, err)
	}
	if matched, err := assistantService.RecordLarkPersonReply(context.Background(), todo.ID, "different-park", reader.observation); err != nil || matched {
		t.Fatalf("different park matched=%v err=%v", matched, err)
	}
	if kinds := personReplyPendingKinds(assistantService, todo.ID); len(kinds) != 1 {
		t.Fatalf("duplicate correlation signals=%v", kinds)
	}
	restarted, err := larkperson.New(settings, transport, reader, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	afterRestart, err := restarted.Deliver(context.Background(), request)
	if err != nil || afterRestart.State != larkperson.StateSent {
		t.Fatalf("restart delivery=%+v err=%v", afterRestart, err)
	}
	if calls, roots := transport.counts(); calls != 1 || roots != 0 {
		t.Fatalf("restart duplicated transport: replies=%d roots=%d", calls, roots)
	}
	if _, err := restarted.CorrelateReply(context.Background(), request.ClientID, assistantService); err != nil {
		t.Fatal(err)
	}
	if kinds := personReplyPendingKinds(assistantService, todo.ID); len(kinds) != 1 {
		t.Fatalf("restart correlation signals=%v", kinds)
	}
	transport.messages = []adapter.Message{
		{MessageID: "om_reply", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: now.Add(-2 * time.Minute).Format(time.RFC3339),
			Content: json.RawMessage(`{"text":"private body"}`), Sender: adapter.Sender{ID: "ou_person", IDType: "open_id", SenderType: "user"}},
		{MessageID: "om_owner", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: now.Add(-time.Minute).Format(time.RFC3339),
			Sender: adapter.Sender{ID: "ou_owner", IDType: "open_id", SenderType: "user"}},
	}
	things, err := restarted.ReadProactive(context.Background(), larkperson.ProactiveRequest{EndpointID: endpoint.ID, EndpointRevision: endpoint.Revision,
		ChatID: "oc_allowed", ThreadID: "omt_task", EventID: "event_integration"})
	if err != nil || len(things) != 1 {
		t.Fatalf("proactive things=%+v err=%v", things, err)
	}
	thing := things[0]
	if thing.Tier != connectorwire.TierFYI || thing.Parent == nil || thing.Parent.Kind != "todo" || thing.Parent.ID != todo.ID ||
		thing.Preview != "" || thing.Context != "" || thing.CanReply {
		t.Fatalf("unsafe proactive thing=%+v", thing)
	}
	if sends := sentToAgent(fakeHub); len(sends) != 0 {
		t.Fatalf("proactive item caused Agent action: %v", sends)
	}
	if calls, roots := transport.counts(); calls != 1 || roots != 0 {
		t.Fatalf("proactive item caused external send: replies=%d roots=%d", calls, roots)
	}
	if got := todoNow(assistantService, todo.ID); got.Supervision.State != reviewPending || got.DoneAt != nil {
		t.Fatalf("proactive item changed task state: %+v", got.Supervision)
	}
}
