package extmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/agents"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/larkperson"
)

type larkPersonReview interface {
	Park(context.Context, assistantwire.LarkPersonPark) (assistantwire.LarkPersonTask, error)
	State(context.Context, string) (assistantwire.LarkPersonTask, error)
	Close(context.Context, assistantwire.LarkPersonClose) (assistantwire.LarkPersonTask, error)
	RecordLarkPersonReply(context.Context, string, string, connectorwire.LarkReplyObservation) (bool, error)
}

type managerLarkPersonReview struct{ manager *Manager }

func (r managerLarkPersonReview) Park(ctx context.Context, request assistantwire.LarkPersonPark) (assistantwire.LarkPersonTask, error) {
	var out assistantwire.LarkPersonTask
	err := r.manager.callAssistant(ctx, assistantwire.MethodLarkPersonPark, request, &out)
	return out, err
}

func (r managerLarkPersonReview) State(ctx context.Context, taskID string) (assistantwire.LarkPersonTask, error) {
	var out assistantwire.LarkPersonTask
	err := r.manager.callAssistant(ctx, assistantwire.MethodLarkPersonState, assistantwire.LarkPersonClose{TaskID: taskID}, &out)
	return out, err
}

func (r managerLarkPersonReview) Close(ctx context.Context, request assistantwire.LarkPersonClose) (assistantwire.LarkPersonTask, error) {
	var out assistantwire.LarkPersonTask
	err := r.manager.callAssistant(ctx, assistantwire.MethodLarkPersonClose, request, &out)
	return out, err
}

func (r managerLarkPersonReview) RecordLarkPersonReply(ctx context.Context, taskID, parkInstance string, observation connectorwire.LarkReplyObservation) (bool, error) {
	var out struct {
		Matched bool `json:"matched"`
	}
	err := r.manager.callAssistant(ctx, assistantwire.MethodLarkPersonRecord,
		assistantwire.LarkPersonReplyRecord{TaskID: taskID, ParkInstance: parkInstance, Observation: observation}, &out)
	return out.Matched, err
}

// larkPersonSource projects one owner-configured external bot task onto the existing agents.Source contract. Raw
// endpoint/topic/message identity stays in this manager and never enters the public session or message models.
type larkPersonSource struct {
	mu       sync.Mutex // one external send/close transition at a time for this endpoint
	service  *larkperson.Service
	endpoint larkperson.Endpoint
	review   larkPersonReview
}

func newLarkPersonSource(service *larkperson.Service, endpoint larkperson.Endpoint, review larkPersonReview) (*larkPersonSource, error) {
	scope := endpoint.Conversation
	if service == nil || review == nil || !agents.ValidBotEndpointID(endpoint.ID) || !endpoint.Enabled || endpoint.Revision < 1 ||
		endpoint.Kind != larkperson.Kind || endpoint.Source != larkperson.Source || endpoint.Brand != "feishu" && endpoint.Brand != "lark" ||
		endpoint.AppID == "" || endpoint.AccountOpenID == "" || endpoint.AccountOpenID == endpoint.PersonOpenID ||
		endpoint.PersonOpenID == "" || endpoint.PersonVerifiedAt.IsZero() || strings.TrimSpace(endpoint.DisplayName) == "" ||
		strings.TrimSpace(endpoint.DisplayName) != endpoint.DisplayName || len(endpoint.DisplayName) > 200 || scope == nil || !scope.HistoryAuthorized || scope.ChatID == "" || scope.ThreadID == "" ||
		scope.ParentMessageID == "" || !slicesContainsString(endpoint.AllowedChatIDs, scope.ChatID) {
		return nil, &agents.Error{Status: http.StatusBadRequest, Msg: "agents: external bot endpoint is not fully configured"}
	}
	return &larkPersonSource{service: service, endpoint: endpoint, review: review}, nil
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (s *larkPersonSource) Name() string { return "bot_" + s.endpoint.ID }

// taskID is the assistant to-do that carries this endpoint's task. It is listed on the To-dos page, so it is an opaque
// digest of the endpoint rather than the endpoint ID itself.
func (s *larkPersonSource) taskID() string {
	digest := sha256.Sum256([]byte("lark_person_task\x00" + s.endpoint.ID))
	return "todo_lark_" + hex.EncodeToString(digest[:10])
}

func (s *larkPersonSource) session(ctx context.Context) (agents.Session, error) {
	state, err := s.review.State(ctx, s.taskID())
	if err != nil {
		return agents.Session{}, &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot task state is unavailable"}
	}
	active := state.Lifecycle == "" || state.Lifecycle == "active"
	row := agents.AgentSession{SessionKey: agents.BotSessionKey, AccountID: s.endpoint.ID, Provider: "feishu_bot",
		Title: s.endpoint.DisplayName, Kind: "bot", CanSend: active, ReviewState: state.ReviewState, Proactive: "unsupported"}
	if active {
		row.Actions = []string{"complete", "abandon"}
	} else {
		row.Lifecycle = state.Lifecycle
	}
	deliveries, err := s.service.ConversationDeliveries(ctx, s.endpoint, s.taskID())
	if err != nil {
		return agents.Session{}, externalBotError(err)
	}
	for _, delivery := range deliveries {
		if at := delivery.UpdatedAt.UTC().UnixMilli(); at > row.RankTimeMs {
			row.RankTimeMs = at
		}
	}
	return agents.Wrap(s.Name(), row, ""), nil
}

func (s *larkPersonSource) Status(ctx context.Context) (agents.Status, error) {
	status := agents.Status{Source: s.Name(), Enabled: true,
		Providers: []agents.AgentProvider{{Provider: "feishu_bot", Connection: "server", Available: true}},
		Accounts:  []agents.AgentAccount{{AccountID: s.endpoint.ID, Provider: "feishu_bot", DisplayName: s.endpoint.DisplayName, Status: "active", SessionCount: 1}}}
	if _, err := s.Sessions(ctx); err != nil {
		status.Error = "agents: external bot is unavailable"
		status.Providers[0].Available = false
		status.Accounts[0].Status = "unavailable"
		return status, err
	}
	return status, nil
}

func (s *larkPersonSource) Sessions(ctx context.Context) ([]agents.Session, error) {
	row, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return []agents.Session{row}, nil
}

func (s *larkPersonSource) Search(context.Context, string) ([]agents.SearchHit, error) {
	return []agents.SearchHit{}, nil
}

func (s *larkPersonSource) Messages(ctx context.Context, key, cursor string, limit int) (*agents.MessagePage, error) {
	if key != agents.BotSessionKey {
		return nil, &agents.Error{Status: http.StatusNotFound, Msg: "agents: unknown external bot endpoint"}
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return nil, &agents.Error{Status: http.StatusBadRequest, Msg: "agents: invalid external bot history limit"}
	}
	if err := s.correlate(ctx); err != nil {
		return nil, err
	}
	deliveries, err := s.service.ConversationDeliveries(ctx, s.endpoint, s.taskID())
	if err != nil {
		return nil, externalBotError(err)
	}
	scope := s.endpoint.Conversation
	history, err := s.service.ReadHistory(ctx, larkperson.HistoryRequest{EndpointID: s.endpoint.ID, EndpointRevision: s.endpoint.Revision,
		ChatID: scope.ChatID, ThreadID: scope.ThreadID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, externalBotError(err)
	}
	messages := make(map[string]adapterMessage, len(history.Messages))
	for _, message := range history.Messages {
		at, parseErr := time.Parse(time.RFC3339Nano, message.CreateTime)
		if parseErr != nil {
			return nil, &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot history is unavailable"}
		}
		messages[message.MessageID] = adapterMessage{message: message, at: at}
	}
	rows := make([]agents.Message, 0, len(deliveries)*2)
	for _, delivery := range deliveries {
		if delivery.State != larkperson.StateSent {
			continue
		}
		if request, ok := messages[delivery.RequestMessageID]; ok {
			row, err := s.projectMessage(request, delivery, "user")
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		if delivery.ReplyMessageID != "" {
			if reply, ok := messages[delivery.ReplyMessageID]; ok {
				row, err := s.projectMessage(reply, delivery, "assistant")
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreateTimeMs == rows[j].CreateTimeMs {
			return rows[i].ExternalID < rows[j].ExternalID
		}
		return rows[i].CreateTimeMs < rows[j].CreateTimeMs
	})
	for i := range rows {
		rows[i].Position = int64(i + 1)
	}
	session, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return &agents.MessagePage{Session: &session, Messages: rows, HasMore: history.HasMore, NextCursor: history.NextCursor}, nil
}

type adapterMessage struct {
	message adapter.Message
	at      time.Time
}

func (s *larkPersonSource) projectMessage(source adapterMessage, delivery larkperson.Reservation, role string) (agents.Message, error) {
	message := source.message
	validSender := false
	if role == "user" {
		validSender = message.MessageID == delivery.RequestMessageID && message.Sender.SenderType == "app" &&
			(message.Sender.IDType == "app_id" && message.Sender.ID == s.endpoint.AppID || message.Sender.AppID == s.endpoint.AppID)
	} else {
		validSender = message.MessageID == delivery.ReplyMessageID && message.Sender.SenderType == "user" &&
			message.Sender.IDType == "open_id" && message.Sender.ID == s.endpoint.PersonOpenID && message.Sender.AppID == ""
	}
	text, ok := larkperson.HistoryText(message)
	if !validSender || !ok {
		return agents.Message{}, &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot history did not match its exact receipt"}
	}
	digest := sha256.Sum256([]byte(s.endpoint.ID + "\x00" + message.MessageID))
	return agents.Message{ExternalID: "lp_" + hex.EncodeToString(digest[:16]), SessionKey: agents.BotSessionKey, Role: role,
		Blocks: []agents.Block{{Type: "text", Text: text}}, CreateTimeMs: source.at.UnixMilli()}, nil
}

func (s *larkPersonSource) correlate(ctx context.Context) error {
	deliveries, err := s.service.ConversationDeliveries(ctx, s.endpoint, s.taskID())
	if err != nil {
		return externalBotError(err)
	}
	for _, delivery := range deliveries {
		if delivery.State != larkperson.StateSent || delivery.ReplyMessageID != "" {
			continue
		}
		_, err := s.service.CorrelateReply(ctx, delivery.ClientID, s.review)
		if errors.Is(err, larkperson.ErrNoReply) {
			continue
		}
		if err != nil {
			return externalBotError(err)
		}
	}
	return nil
}

func (s *larkPersonSource) Send(ctx context.Context, key, clientID, text string) (json.RawMessage, error) {
	if key != agents.BotSessionKey {
		return nil, &agents.Error{Status: http.StatusNotFound, Msg: "agents: unknown external bot endpoint"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clientID, err := agents.NormalizeBotClientID(clientID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" || agents.TextLen(text) > agents.AgentMaxText {
		return nil, &agents.Error{Status: http.StatusBadRequest, Msg: "agents: invalid external bot message"}
	}
	state, err := s.review.State(ctx, s.taskID())
	if err != nil {
		return nil, &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot task state is unavailable"}
	}
	if state.Lifecycle != "" && state.Lifecycle != "active" {
		return nil, &agents.Error{Status: http.StatusConflict, Msg: "agents: external bot task is already closed"}
	}
	// Record any reply that already answers an earlier delivery before this one parks the task again: the assistant
	// matches a reply only to the current park, so a reply left unrecorded here would never reach pending_review or
	// Bots history. Messages() correlates too, but a send can come without a read (another client, an extension, or a
	// reply that arrived since the last poll).
	if err := s.correlate(ctx); err != nil {
		return nil, err
	}
	scope := s.endpoint.Conversation
	parkInstance := "park_" + strings.ReplaceAll(clientID, "-", "_")
	reservation, err := s.service.Deliver(ctx, larkperson.DeliveryRequest{ClientID: clientID, TaskID: s.taskID(), ParkInstance: parkInstance,
		EndpointID: s.endpoint.ID, EndpointRevision: s.endpoint.Revision, Payload: text, ChatID: scope.ChatID,
		ThreadID: scope.ThreadID, ParentMessageID: scope.ParentMessageID})
	if err != nil {
		return nil, externalBotError(err)
	}
	binding, err := s.service.Binding(ctx, reservation.ClientID)
	if err != nil {
		return nil, externalBotError(err)
	}
	_, err = s.review.Park(ctx, assistantwire.LarkPersonPark{TaskID: s.taskID(), ParkInstance: parkInstance,
		Title: s.endpoint.DisplayName, DisplayName: s.endpoint.DisplayName, Watch: binding.Watch, URL: binding.URL, SentAt: binding.SentAt})
	if err != nil {
		return nil, &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot reply review is unavailable; retry only with the same clientId"}
	}
	return json.RawMessage(`{"accepted":true}`), nil
}

func (s *larkPersonSource) Action(ctx context.Context, key, action string) (*agents.Session, error) {
	if key != agents.BotSessionKey {
		return nil, &agents.Error{Status: http.StatusNotFound, Msg: "agents: unknown external bot endpoint"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if action != "complete" && action != "abandon" {
		return nil, &agents.Error{Status: http.StatusBadRequest, Msg: "agents: unsupported external bot action"}
	}
	if _, err := s.review.Close(ctx, assistantwire.LarkPersonClose{TaskID: s.taskID(), Action: action}); err != nil {
		return nil, &agents.Error{Status: http.StatusConflict, Msg: "agents: external bot task could not be closed"}
	}
	if err := s.service.ReleaseConversation(ctx, s.endpoint, s.taskID()); err != nil {
		return nil, externalBotError(err)
	}
	session, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *larkPersonSource) DeleteMessage(_ context.Context, key, _ string) error {
	if key != agents.BotSessionKey {
		return &agents.Error{Status: http.StatusNotFound, Msg: "agents: unknown external bot endpoint"}
	}
	return &agents.Error{Status: http.StatusNotImplemented, Msg: "agents: external bot history cannot delete messages"}
}

func (s *larkPersonSource) MarkRead(_ context.Context, key string) error {
	if key != agents.BotSessionKey {
		return &agents.Error{Status: http.StatusNotFound, Msg: "agents: unknown external bot endpoint"}
	}
	return nil
}

func externalBotError(err error) error {
	switch {
	case errors.Is(err, larkperson.ErrConflict):
		return &agents.Error{Status: http.StatusConflict, Msg: "agents: external bot operation conflicts with its frozen task or topic"}
	case errors.Is(err, larkperson.ErrUnknown):
		return &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot outcome is unknown; retry only with the same clientId"}
	case errors.Is(err, larkperson.ErrUnauthorized):
		return &agents.Error{Status: http.StatusForbidden, Msg: "agents: external bot exact source scope is not authorized"}
	case errors.Is(err, larkperson.ErrUnsupported), errors.Is(err, larkperson.ErrInvalid):
		return &agents.Error{Status: http.StatusBadRequest, Msg: "agents: invalid external bot request"}
	default:
		return &agents.Error{Status: http.StatusBadGateway, Msg: "agents: external bot is unavailable"}
	}
}

// initLarkPersonSources runs after connectors start. Every valid endpoint joins the same agents.Service as Orca and
// plugin sources; there is no second registry, transcript store, or Feishu-specific HTTP chat stack.
func (h *Manager) initLarkPersonSources(ctx context.Context) {
	service, err := h.LarkPersonService()
	if err != nil {
		return
	}
	endpoints, err := service.Endpoints(ctx)
	if err != nil {
		h.logf("agents: external bot endpoints are unavailable")
		return
	}
	review := managerLarkPersonReview{manager: h}
	for _, endpoint := range endpoints {
		source, err := newLarkPersonSource(service, endpoint, review)
		if err != nil {
			h.logf("agents: external bot endpoint %q is not registered", endpoint.ID)
			continue
		}
		if !h.agents.Add(source) {
			h.logf("agents: external bot endpoint %q duplicates another source", endpoint.ID)
		}
	}
}
