package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/agents"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/larkperson"
)

type conversationSettings struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func newConversationSettings() *conversationSettings {
	return &conversationSettings{values: map[string]json.RawMessage{}}
}

func (s *conversationSettings) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (s *conversationSettings) PutSetting(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.values[key] = raw
	s.mu.Unlock()
	return nil
}

func (s *conversationSettings) CompareSetting(_ context.Context, key string, revision int64, value any) (bool, error) {
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

func (s *conversationSettings) SettingsPrefix(_ context.Context, prefix string) (map[string]json.RawMessage, error) {
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

type conversationTransport struct {
	mu        sync.Mutex
	now       time.Time
	auth      adapter.AuthStatus
	messages  []adapter.Message
	listCalls []larkperson.Topic
	sends     int
	replyErr  error
	hasMore   bool
}

func newConversationTransport(now time.Time) *conversationTransport {
	return &conversationTransport{now: now, auth: adapter.AuthStatus{Brand: "feishu", AppID: "cli_synthetic", OpenID: "ou_bot", LoggedIn: true}}
}

func (f *conversationTransport) AuthStatus(context.Context) (adapter.AuthStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth, nil
}

func (f *conversationTransport) SearchPeople(context.Context, string, int) ([]larkperson.Person, error) {
	return []larkperson.Person{{OpenID: "ou_external", Name: "Synthetic collaborator"}}, nil
}

func (f *conversationTransport) ListMessages(_ context.Context, chatID, threadID, _ string, _ int) (adapter.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, larkperson.Topic{ChatID: chatID, ThreadID: threadID})
	return adapter.MessagePage{Messages: append([]adapter.Message(nil), f.messages...), HasMore: f.hasMore, PageToken: "private_provider_cursor"}, nil
}

func (f *conversationTransport) SendText(context.Context, string, string, string) (json.RawMessage, error) {
	return nil, errors.New("root delivery is forbidden")
}

func (f *conversationTransport) ReplyText(_ context.Context, parent, text, _ string, inThread bool) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replyErr != nil {
		return nil, f.replyErr
	}
	if !inThread || parent != "om_parent" {
		return nil, errors.New("exact existing parent and thread required")
	}
	f.sends++
	id := fmt.Sprintf("om_request_%d", f.sends)
	at := f.now.Add(-2 * time.Second).Format(time.RFC3339)
	f.messages = append(f.messages, adapter.Message{MessageID: id, ChatID: "oc_scoped", ThreadID: "omt_scoped", MsgType: "text",
		CreateTime: at, Content: json.RawMessage(fmt.Sprintf(`{"text":%q}`, text)),
		MessageAppLink: "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_scoped&open_thread_id=omt_scoped&message_id=" + id,
		Sender:         adapter.Sender{ID: "cli_synthetic", IDType: "app_id", SenderType: "app", AppID: "cli_synthetic"}})
	return json.RawMessage(fmt.Sprintf(`{"data":{"message_id":%q}}`, id)), nil
}

func (f *conversationTransport) addReply(id, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, adapter.Message{MessageID: id, ChatID: "oc_scoped", ThreadID: "omt_scoped", MsgType: "text",
		CreateTime: f.now.Add(-time.Second).Format(time.RFC3339), Content: json.RawMessage(fmt.Sprintf(`{"text":%q}`, body)),
		Sender: adapter.Sender{ID: "ou_external", IDType: "open_id", SenderType: "user"}})
}

type conversationReader struct{ transport *conversationTransport }

func (r conversationReader) ObserveReply(_ context.Context, watch connectorwire.LarkReplyWatch) (connectorwire.LarkReplyObservation, error) {
	r.transport.mu.Lock()
	defer r.transport.mu.Unlock()
	out := connectorwire.LarkReplyObservation{Watch: watch, CheckedAt: r.transport.now}
	for _, message := range r.transport.messages {
		if message.ChatID != watch.ChatID || message.ThreadID != watch.ThreadID {
			continue
		}
		at, err := time.Parse(time.RFC3339, message.CreateTime)
		if err != nil {
			return out, err
		}
		receipt := connectorwire.LarkReplyMessage{ID: message.MessageID, SenderID: message.Sender.ID, SenderType: message.Sender.SenderType,
			IDType: message.Sender.IDType, AppID: message.Sender.AppID, ChatID: message.ChatID, ThreadID: message.ThreadID,
			URL: message.MessageAppLink, At: at, Before: at.Add(time.Second)}
		if message.MessageID == watch.RequestMessageID {
			out.Request = receipt
		}
		if message.Sender.ID == watch.PersonOpenID && message.Sender.SenderType == "user" {
			out.Reply = receipt
		}
	}
	return out, nil
}

type fakeLarkPersonReview struct {
	mu      sync.Mutex
	parks   []assistantwire.LarkPersonPark
	records int
	task    assistantwire.LarkPersonTask
	close   []string
}

func (r *fakeLarkPersonReview) Park(_ context.Context, request assistantwire.LarkPersonPark) (assistantwire.LarkPersonTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prior := range r.parks {
		if prior.ParkInstance == request.ParkInstance {
			if prior.TaskID != request.TaskID || prior.Watch != request.Watch {
				return assistantwire.LarkPersonTask{}, errors.New("park conflict")
			}
			return r.task, nil
		}
	}
	r.parks = append(r.parks, request)
	r.task = assistantwire.LarkPersonTask{Lifecycle: "active"}
	return r.task, nil
}

func (r *fakeLarkPersonReview) State(context.Context, string) (assistantwire.LarkPersonTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.Lifecycle == "" {
		r.task.Lifecycle = "active"
	}
	return r.task, nil
}

func (r *fakeLarkPersonReview) Close(_ context.Context, request assistantwire.LarkPersonClose) (assistantwire.LarkPersonTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.close = append(r.close, request.Action)
	r.task.Lifecycle = "completed"
	if request.Action == "abandon" {
		r.task.Lifecycle = "abandoned"
	}
	r.task.ReviewState = ""
	return r.task, nil
}

func (r *fakeLarkPersonReview) RecordLarkPersonReply(_ context.Context, taskID, parkInstance string, _ connectorwire.LarkReplyObservation) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for _, park := range r.parks {
		found = found || park.TaskID == taskID && park.ParkInstance == parkInstance
	}
	if !found {
		return false, nil
	}
	r.records++
	r.task.ReviewState = "pending_review"
	return true, nil
}

type conversationFixture struct {
	service   *larkperson.Service
	settings  *conversationSettings
	transport *conversationTransport
	review    *fakeLarkPersonReview
	endpoint  larkperson.Endpoint
}

func newConversationFixture(t *testing.T) conversationFixture {
	t.Helper()
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	settings := newConversationSettings()
	transport := newConversationTransport(now)
	transport.messages = []adapter.Message{{MessageID: "om_parent", ChatID: "oc_scoped", ThreadID: "omt_scoped", MsgType: "text",
		CreateTime: now.Add(-time.Minute).Format(time.RFC3339)}}
	service, err := larkperson.New(settings, transport, conversationReader{transport: transport}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateEndpoint(context.Background(), larkperson.Endpoint{
		ID: "external_task", Kind: larkperson.Kind, DisplayName: "Synthetic collaborator", Source: larkperson.Source,
		Brand: "feishu", AppID: "cli_synthetic", AccountOpenID: "ou_bot", AllowedChatIDs: []string{"oc_scoped"}, Enabled: true,
		Conversation: &larkperson.ConversationScope{ChatID: "oc_scoped", ThreadID: "omt_scoped", ParentMessageID: "om_parent", HistoryAuthorized: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := service.VerifyPerson(context.Background(), created.ID, created.DisplayName, "ou_external")
	if err != nil {
		t.Fatal(err)
	}
	return conversationFixture{service: service, settings: settings, transport: transport, review: &fakeLarkPersonReview{}, endpoint: endpoint}
}

func (f conversationFixture) source(t *testing.T) *larkPersonSource {
	t.Helper()
	source, err := newLarkPersonSource(f.service, f.endpoint, f.review)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestLarkPersonEndpointProjectionIsScopedAndDisplaySafe(t *testing.T) {
	f := newConversationFixture(t)
	valid := f.source(t)
	if valid.Name() != "bot_external_task" {
		t.Fatalf("stable source name %q", valid.Name())
	}
	rows, err := valid.Sessions(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("projected sessions %+v %v", rows, err)
	}
	row := rows[0]
	if row.ID != "bot_external_task:endpoint" || row.Provider != "feishu_bot" || row.Title != "Synthetic collaborator" ||
		!row.CanSend || row.Proactive != "unsupported" || !slicesContains(row.Actions, "complete") || !slicesContains(row.Actions, "abandon") {
		t.Fatalf("display-safe projection %+v", row)
	}
	raw, _ := json.Marshal(row)
	for _, private := range []string{"oc_scoped", "omt_scoped", "om_parent", "ou_external", "ou_bot", "cli_synthetic", "todo_"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private endpoint data escaped in %s", raw)
		}
	}
	for _, endpoint := range []larkperson.Endpoint{
		{ID: "disabled", Enabled: false},
		{ID: "unscoped", Enabled: true},
		{ID: "not valid", Enabled: true},
	} {
		if _, err := newLarkPersonSource(f.service, endpoint, f.review); err == nil {
			t.Fatalf("invalid endpoint projected: %+v", endpoint)
		}
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestLarkPersonConversationAssignmentReplyFollowupAndCompletion(t *testing.T) {
	f := newConversationFixture(t)
	source := f.source(t)
	service := agents.NewService(f.settings, nil, nil, source)
	ctx := context.Background()
	id := "bot_external_task:endpoint"
	firstID := "11111111-1111-4111-8111-111111111111"
	result, err := service.Send(ctx, id, firstID, "First synthetic assignment", "test")
	if err != nil || result.Entry.Status != agents.OutSent || string(result.Upstream) != `{"accepted":true}` {
		t.Fatalf("first assignment result %+v err %v", result, err)
	}
	if f.transport.sends != 1 || len(f.review.parks) != 1 || f.review.task.Lifecycle == "completed" {
		t.Fatalf("accepted send was completion or missing park: sends=%d parks=%+v task=%+v", f.transport.sends, f.review.parks, f.review.task)
	}
	page, err := service.Messages(ctx, id, "", 50)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Role != "user" || page.Messages[0].Blocks[0].Text != "First synthetic assignment" {
		t.Fatalf("readback history %+v err %v", page, err)
	}
	if f.review.task.ReviewState == "pending_review" {
		t.Fatal("send receipt was fabricated into a reply review")
	}

	f.transport.addReply("om_reply_1", "Exact synthetic reply")
	service.Updated(source.Name(), []string{agents.BotSessionKey})
	page, err = service.Messages(ctx, id, "", 50)
	if err != nil || len(page.Messages) != 2 || page.Messages[1].Role != "assistant" || page.Messages[1].Blocks[0].Text != "Exact synthetic reply" {
		t.Fatalf("exact reply history %+v err %v", page, err)
	}
	if f.review.records != 1 || f.review.task.ReviewState != "pending_review" {
		t.Fatalf("exact reply was not pending review: records=%d task=%+v", f.review.records, f.review.task)
	}
	raw, _ := json.Marshal(page)
	for _, private := range []string{"oc_scoped", "omt_scoped", "om_reply_1", "om_request_1", "ou_external", "cli_synthetic", "todo_"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private source identity escaped history: %s", raw)
		}
	}
	if _, err := service.Messages(ctx, id, "", 50); err != nil || f.review.records != 1 {
		t.Fatalf("duplicate history enqueued another review: records=%d err=%v", f.review.records, err)
	}

	followID := "22222222-2222-4222-8222-222222222222"
	if _, err := service.Send(ctx, id, followID, "Same-task follow-up", "test"); err != nil {
		t.Fatalf("first reply silently released the topic: %v", err)
	}
	if f.transport.sends != 2 || len(f.review.parks) != 2 || f.review.parks[0].TaskID != f.review.parks[1].TaskID ||
		f.review.parks[0].ParkInstance == f.review.parks[1].ParkInstance {
		t.Fatalf("follow-up did not preserve task and create a fresh park: %+v", f.review.parks)
	}
	actionSource, ok := any(source).(agents.ActionSource)
	if !ok {
		t.Fatal("Feishu source has no generic lifecycle actions")
	}
	closed, err := actionSource.Action(ctx, agents.BotSessionKey, "complete")
	if err != nil || closed == nil || closed.CanSend || closed.Lifecycle != "completed" || len(f.review.close) != 1 || f.review.close[0] != "complete" {
		t.Fatalf("completion action %+v close=%v err=%v", closed, f.review.close, err)
	}
	if _, err := service.Send(ctx, id, "33333333-3333-4333-8333-333333333333", "Closed task must not send", "test"); err == nil {
		t.Fatal("completed conversation accepted another send")
	}
	other := larkperson.DeliveryRequest{ClientID: "client_other", TaskID: "todo_other", ParkInstance: "park_other",
		EndpointID: f.endpoint.ID, EndpointRevision: f.endpoint.Revision, Payload: "Different synthetic task",
		ChatID: "oc_scoped", ThreadID: "omt_scoped", ParentMessageID: "om_parent"}
	if _, err := f.service.Deliver(ctx, other); err != nil {
		t.Fatalf("completion did not release the topic for a different task: %v", err)
	}
}

func TestLarkPersonConversationAbandonmentAndWrongScope(t *testing.T) {
	f := newConversationFixture(t)
	source := f.source(t)
	ctx := context.Background()
	if _, err := source.Send(ctx, agents.BotSessionKey, "44444444-4444-4444-8444-444444444444", "Synthetic assignment"); err != nil {
		t.Fatal(err)
	}
	action := any(source).(agents.ActionSource)
	if _, err := action.Action(ctx, agents.BotSessionKey, "abandon"); err != nil {
		t.Fatal(err)
	}
	if len(f.review.close) != 1 || f.review.close[0] != "abandon" {
		t.Fatalf("abandonment close=%v", f.review.close)
	}
	other := larkperson.DeliveryRequest{ClientID: "client_after_abandon", TaskID: "todo_other", ParkInstance: "park_other",
		EndpointID: f.endpoint.ID, EndpointRevision: f.endpoint.Revision, Payload: "Different synthetic task",
		ChatID: "oc_scoped", ThreadID: "omt_scoped", ParentMessageID: "om_parent"}
	if _, err := f.service.Deliver(ctx, other); err != nil {
		t.Fatalf("abandonment did not release topic: %v", err)
	}

	wrong := newConversationFixture(t)
	wrongSource := wrong.source(t)
	wrong.transport.auth.AppID = "cli_wrong"
	before := wrong.transport.sends
	if _, err := wrongSource.Send(ctx, agents.BotSessionKey, "66666666-6666-4666-8666-666666666666", "Wrong bot identity"); err == nil || wrong.transport.sends != before {
		t.Fatalf("wrong bot identity reached transport or was hidden: sends=%d err=%v", wrong.transport.sends, err)
	}
	wrong.transport.auth.AppID = "cli_synthetic"
	wrong.transport.messages = append(wrong.transport.messages, adapter.Message{MessageID: "om_wrong_topic", ChatID: "oc_other", ThreadID: "omt_other", MsgType: "text",
		CreateTime: wrong.transport.now.Add(-time.Second).Format(time.RFC3339), Content: json.RawMessage(`{"text":"must not escape"}`),
		Sender: adapter.Sender{ID: "ou_external", IDType: "open_id", SenderType: "user"}})
	if page, err := wrongSource.Messages(ctx, agents.BotSessionKey, "", 50); err == nil || page != nil && len(page.Messages) != 0 {
		t.Fatalf("out-of-scope history was returned: %+v err=%v", page, err)
	}
}

func TestLarkPersonConversationDuplicateRestartAndConcurrentDelivery(t *testing.T) {
	f := newConversationFixture(t)
	source := f.source(t)
	ctx := context.Background()
	clientID := "77777777-7777-4777-8777-777777777777"
	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := source.Send(ctx, agents.BotSessionKey, clientID, "Concurrent synthetic assignment")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !strings.Contains(err.Error(), "same clientId") {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if f.transport.sends != 1 {
		t.Fatalf("concurrent duplicate sent %d messages", f.transport.sends)
	}
	restarted, err := larkperson.New(f.settings, f.transport, conversationReader{transport: f.transport}, func() time.Time { return f.transport.now })
	if err != nil {
		t.Fatal(err)
	}
	restartedSource, err := newLarkPersonSource(restarted, f.endpoint, f.review)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedSource.Send(ctx, agents.BotSessionKey, clientID, "Concurrent synthetic assignment"); err != nil {
		t.Fatalf("restart retry did not reuse proven reservation: %v", err)
	}
	if f.transport.sends != 1 {
		t.Fatalf("restart duplicate sent %d messages", f.transport.sends)
	}
	if _, err := restartedSource.Send(ctx, agents.BotSessionKey, clientID, "Changed payload"); agents.HTTPStatus(err) != http.StatusConflict || f.transport.sends != 1 {
		t.Fatalf("same-ID changed payload was not refused before transport: %v sends=%d", err, f.transport.sends)
	}
}

func TestLarkPersonHistoryScopeAndProactiveFailClosed(t *testing.T) {
	f := newConversationFixture(t)
	source := f.source(t)
	ctx := context.Background()
	if _, err := source.Messages(ctx, agents.BotSessionKey, "private_provider_cursor", 50); agents.HTTPStatus(err) != http.StatusBadRequest || len(f.transport.listCalls) != 0 {
		t.Fatalf("provider cursor was accepted or reached transport: err=%v calls=%d", err, len(f.transport.listCalls))
	}
	if _, err := source.Send(ctx, agents.BotSessionKey, "88888888-8888-4888-8888-888888888888", "Scoped assignment"); err != nil {
		t.Fatal(err)
	}
	f.transport.addReply("om_unmatched_proactive", "Body-free proactive message")
	f.transport.addReply("om_second_proactive", "Second proactive message")
	page, err := source.Messages(ctx, agents.BotSessionKey, "", 50)
	if err != nil || len(page.Messages) != 2 || page.Messages[1].Blocks[0].Text != "Second proactive message" {
		t.Fatalf("exact history included unmatched proactive bodies or missed the exact reply: %+v %v", page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "Body-free proactive message") {
		t.Fatalf("unmatched proactive body escaped into conversation history: %s", raw)
	}
	things, err := f.service.ReadProactive(ctx, larkperson.ProactiveRequest{EndpointID: f.endpoint.ID, EndpointRevision: f.endpoint.Revision,
		ChatID: "oc_scoped", ThreadID: "omt_scoped", EventID: "event_multi"})
	if err == nil || len(things) != 0 {
		t.Fatalf("standing multi-message proactive ingestion was enabled: things=%+v err=%v", things, err)
	}
	f.transport.hasMore = true
	if page, err := source.Messages(ctx, agents.BotSessionKey, "", 50); err == nil || page != nil {
		t.Fatalf("long history did not fail closed: %+v err=%v", page, err)
	}
	f.transport.hasMore = false
	for _, call := range f.transport.listCalls {
		if call.ChatID != "oc_scoped" || call.ThreadID != "omt_scoped" {
			t.Fatalf("history escaped exact scope: %+v", call)
		}
	}
	if hits, err := source.Search(ctx, "anything"); err != nil || len(hits) != 0 {
		t.Fatalf("broad search enabled: %+v %v", hits, err)
	}
	if err := source.DeleteMessage(ctx, agents.BotSessionKey, "anything"); agents.HTTPStatus(err) != http.StatusNotImplemented {
		t.Fatalf("source history deletion unexpectedly supported: %v", err)
	}
}
