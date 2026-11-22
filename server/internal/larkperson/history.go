package larkperson

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// ReadHistory performs a separately authorized body read of one exact existing topic. It neither returns another chat
// nor turns a receipt into history. Scope and identity are checked on both sides of the transport read.
func (s *Service) ReadHistory(ctx context.Context, request HistoryRequest) (HistoryPage, error) {
	if request.Limit == 0 {
		request.Limit = 50
	}
	if request.Limit < 1 || request.Limit > 50 || request.Cursor != "" {
		return HistoryPage{}, ErrInvalid
	}
	if _, err := s.authorizeConversation(ctx, request); err != nil {
		return HistoryPage{}, err
	}
	page, err := s.transport.ListMessages(ctx, request.ChatID, request.ThreadID, request.Cursor, request.Limit)
	if err != nil || page.HasMore {
		return HistoryPage{}, ErrReadback
	}
	for i := range page.Messages {
		message := &page.Messages[i]
		if message.ChatID != request.ChatID || message.ThreadID != request.ThreadID {
			return HistoryPage{}, ErrReadback
		}
		at, _, ok := sourceInterval(message.CreateTime, s.clock())
		if !ok {
			return HistoryPage{}, ErrReadback
		}
		message.CreateTime = at.Format(time.RFC3339Nano)
	}
	if _, err := s.authorizeConversation(ctx, request); err != nil {
		return HistoryPage{}, err
	}
	return HistoryPage{Messages: append([]adapter.Message(nil), page.Messages...), HasMore: page.HasMore, NextCursor: page.PageToken}, nil
}

func (s *Service) authorizeConversation(ctx context.Context, request HistoryRequest) (Endpoint, error) {
	if !validOpaqueID(request.EndpointID) || request.EndpointRevision < 1 || !validOpaqueID(request.ChatID) || !validOpaqueID(request.ThreadID) {
		return Endpoint{}, ErrInvalid
	}
	endpoint, err := s.Endpoint(ctx, request.EndpointID)
	if err != nil {
		return Endpoint{}, err
	}
	scope := endpoint.Conversation
	if endpoint.Revision != request.EndpointRevision || !endpoint.Enabled || endpoint.PersonOpenID == "" || endpoint.PersonVerifiedAt.IsZero() ||
		scope == nil || !scope.HistoryAuthorized || scope.ChatID != request.ChatID || scope.ThreadID != request.ThreadID ||
		!chatAllowed(endpoint, request.ChatID) {
		return Endpoint{}, ErrUnauthorized
	}
	if err := s.authorizeIdentity(ctx, endpoint); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

// ConversationDeliveries returns only the frozen reservations of one task in one exact endpoint topic. It is an
// internal correlation index, not a public history store; PayloadHash stays inside the adapter.
func (s *Service) ConversationDeliveries(ctx context.Context, endpoint Endpoint, taskID string) ([]Reservation, error) {
	if endpoint.Conversation == nil || !validOpaqueID(endpoint.ID) || !validOpaqueID(taskID) {
		return nil, ErrInvalid
	}
	values, err := s.store.settings.SettingsPrefix(ctx, deliveryPrefix)
	if err != nil {
		return nil, ErrStore
	}
	scope := endpoint.Conversation
	out := make([]Reservation, 0)
	for _, raw := range values {
		var reservation Reservation
		if json.Unmarshal(raw, &reservation) != nil {
			return nil, ErrStore
		}
		if reservation.EndpointID != endpoint.ID || reservation.EndpointRevision != endpoint.Revision || reservation.TaskID != taskID ||
			reservation.Topic.ChatID != scope.ChatID || reservation.Topic.ThreadID != scope.ThreadID {
			continue
		}
		out = append(out, reservation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ClientID < out[j].ClientID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if len(out) > 200 {
		return nil, ErrInvalid
	}
	return out, nil
}

// ReleaseConversation releases only the current holder of this exact task and topic. A superseded or different task
// cannot release another task's claim; repeated completion after a successful release is a no-op.
func (s *Service) ReleaseConversation(ctx context.Context, endpoint Endpoint, taskID string) error {
	if endpoint.Conversation == nil || !validOpaqueID(endpoint.ID) || !validOpaqueID(taskID) {
		return ErrInvalid
	}
	scope := endpoint.Conversation
	claim, found, err := s.store.topic(ctx, endpoint, Topic{ChatID: scope.ChatID, ThreadID: scope.ThreadID})
	if err != nil {
		return ErrStore
	}
	if !found || !claim.Active {
		return nil
	}
	if claim.EndpointID != endpoint.ID || claim.TaskID != taskID {
		return nil // a superseded claim is not this task's to release
	}
	return s.ReleaseTopic(ctx, claim.ClientID)
}

// HistoryText decodes only a plain text body from an already authorized exact-topic message. Other message types and
// malformed or oversized bodies are not projected into the generic transcript.
func HistoryText(message adapter.Message) (string, bool) {
	if message.Deleted || message.MsgType != "text" {
		return "", false
	}
	return historyTextRaw(message.Content, 0)
}

func historyTextRaw(raw json.RawMessage, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}
	var object struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &object); err == nil && object.Text != nil {
		return validHistoryText(*object.Text)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		var nested json.RawMessage
		if json.Unmarshal([]byte(encoded), &nested) == nil {
			return historyTextRaw(nested, depth+1)
		}
		return validHistoryText(encoded)
	}
	return "", false
}

func validHistoryText(text string) (string, bool) {
	return text, strings.TrimSpace(text) != "" && utf8.ValidString(text) && len(text) <= 1<<20
}
