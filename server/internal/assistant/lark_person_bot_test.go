package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/larkperson"
)

func TestLarkPersonBotOpaqueParkPendingReviewAndClose(t *testing.T) {
	assistantService, fakeHub, _ := newSupervisedService(t)
	holdDigest(assistantService)
	now := time.Now().UTC().Truncate(time.Second)
	deliveryNow := now.Add(-10 * time.Minute)
	clock := deliveryNow
	settings := newIntegrationPersonSettings()
	transport := &integrationPersonTransport{auth: adapter.AuthStatus{Brand: "feishu", AppID: "cli_bot", OpenID: "ou_owner", LoggedIn: true},
		people: []larkperson.Person{{OpenID: "ou_person", Name: "Synthetic collaborator"}}}
	requestAt := deliveryNow.Add(-time.Second)
	topicURL := "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_allowed&open_thread_id=omt_task&message_id=om_request"
	transport.messages = []adapter.Message{
		{MessageID: "om_parent", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: deliveryNow.Add(-time.Minute).Format(time.RFC3339)},
		{MessageID: "om_request", ChatID: "oc_allowed", ThreadID: "omt_task", MsgType: "text", CreateTime: requestAt.Format(time.RFC3339), MessageAppLink: topicURL,
			Sender: adapter.Sender{ID: "cli_bot", IDType: "app_id", SenderType: "app", AppID: "cli_bot"}},
	}
	reader := &integrationPersonReader{}
	larkService, err := larkperson.New(settings, transport, reader, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	created, err := larkService.CreateEndpoint(context.Background(), larkperson.Endpoint{ID: "endpoint_bot", Kind: larkperson.Kind,
		DisplayName: "Synthetic collaborator", Source: larkperson.Source, Brand: "feishu", AppID: "cli_bot", AccountOpenID: "ou_owner",
		AllowedChatIDs: []string{"oc_allowed"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := larkService.VerifyPerson(context.Background(), created.ID, created.DisplayName, "ou_person")
	if err != nil {
		t.Fatal(err)
	}
	request := larkperson.DeliveryRequest{ClientID: "client_bot", TaskID: "todo_lark_external_task", ParkInstance: "park_opaque",
		EndpointID: endpoint.ID, EndpointRevision: endpoint.Revision, Payload: "Synthetic task payload", ChatID: "oc_allowed",
		ThreadID: "omt_task", ParentMessageID: "om_parent"}
	if _, err := larkService.Deliver(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	binding, err := larkService.Binding(context.Background(), request.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	parkRequest := assistantwire.LarkPersonPark{TaskID: request.TaskID, ParkInstance: request.ParkInstance,
		Title: "Synthetic bot task", DisplayName: endpoint.DisplayName, Watch: binding.Watch, URL: binding.URL, SentAt: binding.SentAt}
	state, err := assistantService.ParkLarkPersonTask(context.Background(), parkRequest)
	if err != nil || state.Lifecycle != "active" || state.ReviewState != "" {
		t.Fatalf("park state=%+v err=%v", state, err)
	}
	parked := todoNow(assistantService, request.TaskID).Supervision.parking()
	if parked == nil || parked.Evidence.ParkInstance != request.ParkInstance || !parked.At.After(binding.SentAt) {
		t.Fatalf("opaque park was not recorded after readback: %+v", parked)
	}
	checked := time.Now().UTC()
	replyAt := parked.At.Add(time.Nanosecond)
	clock = checked
	reader.observation = connectorwire.LarkReplyObservation{Watch: binding.Watch,
		Request: connectorwire.LarkReplyMessage{ID: "om_request", SenderID: "cli_bot", SenderType: "app", IDType: "app_id", AppID: "cli_bot",
			ChatID: "oc_allowed", ThreadID: "omt_task", URL: binding.URL, At: binding.SentAt, Before: binding.SentAt.Add(time.Second)},
		Reply: connectorwire.LarkReplyMessage{ID: "om_reply", SenderID: "ou_person", SenderType: "user", IDType: "open_id",
			ChatID: "oc_allowed", ThreadID: "omt_task", At: replyAt, Before: replyAt.Add(time.Nanosecond)}, CheckedAt: checked}
	if _, err := larkService.CorrelateReply(context.Background(), request.ClientID, assistantService); err != nil {
		t.Fatalf("correlate opaque park: %v", err)
	}
	state, err = assistantService.LarkPersonTaskState(context.Background(), request.TaskID)
	if err != nil || state.ReviewState != "pending_review" {
		t.Fatalf("pending review state=%+v err=%v", state, err)
	}
	if kinds := personReplyPendingKinds(assistantService, request.TaskID); len(kinds) != 1 || kinds[0] != "review" {
		t.Fatalf("pending signals=%v", kinds)
	}
	// GET /api/v1/todos shows the task without its raw source identity; the in-memory evidence keeps it for matching.
	listed, err := assistantService.listedTodosOf(context.Background(), false, map[string]bool{request.TaskID: true})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed to-dos=%d err=%v", len(listed), err)
	}
	raw, err := json.Marshal(listed[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"oc_allowed", "omt_task", "om_request", "om_reply", "ou_person", "ou_owner", "cli_bot", "applink", request.ParkInstance} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("to-dos API exposed %q: %s", private, raw)
		}
	}
	// Routes that answer the whole to-do ({"todo": t}: seen, thread, marks, route) redact it too.
	whole, err := json.Marshal(map[string]any{"todo": todoNow(assistantService, request.TaskID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"oc_allowed", "omt_task", "om_request", "om_reply", "ou_person", "ou_owner", "cli_bot", "applink", request.ParkInstance} {
		if strings.Contains(string(whole), private) {
			t.Fatalf("a whole to-do response exposed %q: %s", private, whole)
		}
	}
	// Storage marshals the reviews apart (Supervision.row) and keeps the evidence reply correlation needs.
	stored := todoNow(assistantService, request.TaskID).Supervision.row(request.TaskID)
	if !strings.Contains(string(stored.Reviews), "om_request") || !strings.Contains(string(stored.Reviews), request.ParkInstance) {
		t.Fatalf("the stored ledger lost its reply evidence: %s", stored.Reviews)
	}
	watchKept, observationKept := false, false
	for _, review := range todoNow(assistantService, request.TaskID).Supervision.Reviews {
		watchKept = watchKept || review.Evidence != nil && review.Evidence.Reply != nil
		observationKept = observationKept || review.Reply != nil
	}
	if !watchKept || !observationKept {
		t.Fatal("redaction removed the server-side reply watch or observation")
	}
	if sends := sentToAgent(fakeHub); len(sends) != 0 {
		t.Fatalf("exact reply became an Agent instruction: %v", sends)
	}
	if matched, err := assistantService.RecordLarkPersonReply(context.Background(), request.TaskID, "different_park", reader.observation); err != nil || matched {
		t.Fatalf("wrong park instance matched=%v err=%v", matched, err)
	}
	state, err = assistantService.ParkLarkPersonTask(context.Background(), parkRequest)
	if err != nil || state.ReviewState != "pending_review" || len(todoNow(assistantService, request.TaskID).Supervision.Reviews) != 1 {
		t.Fatalf("idempotent re-park erased pending review: state=%+v err=%v", state, err)
	}
	if _, err := larkService.CorrelateReply(context.Background(), request.ClientID, assistantService); err != nil {
		t.Fatal(err)
	}
	if kinds := personReplyPendingKinds(assistantService, request.TaskID); len(kinds) != 1 {
		t.Fatalf("duplicate correlation signals=%v", kinds)
	}
	state, err = assistantService.CloseLarkPersonTask(context.Background(), assistantwire.LarkPersonClose{TaskID: request.TaskID, Action: "complete"})
	if err != nil || state.Lifecycle != "completed" {
		t.Fatalf("completion state=%+v err=%v", state, err)
	}
	if done, ok := assistantService.todoNow(request.TaskID); !ok || done.Supervision == nil || done.Supervision.State != reviewAccepted {
		t.Fatalf("completed bot task kept a non-accepted review state: %+v", done.Supervision)
	}
	if state, err = assistantService.CloseLarkPersonTask(context.Background(), assistantwire.LarkPersonClose{TaskID: request.TaskID, Action: "complete"}); err != nil || state.Lifecycle != "completed" {
		t.Fatalf("idempotent completion state=%+v err=%v", state, err)
	}

	if _, err := assistantService.createTaskTodoAs(context.Background(), "todo_lark_abandon", "Synthetic abandoned task", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	state, err = assistantService.CloseLarkPersonTask(context.Background(), assistantwire.LarkPersonClose{TaskID: "todo_lark_abandon", Action: "abandon"})
	if err != nil || state.Lifecycle != "abandoned" {
		t.Fatalf("abandonment state=%+v err=%v", state, err)
	}
	if done, ok := assistantService.todoNow("todo_lark_abandon"); !ok || done.Supervision == nil || done.Supervision.State != reviewCancelled {
		t.Fatalf("abandoned bot task is not cancelled for the To-dos page: %+v", done.Supervision)
	}
}
