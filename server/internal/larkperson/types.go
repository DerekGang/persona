// Package larkperson implements owner-managed Feishu external-collaborator addresses.
// It does not create Persona users, devices, Agent grants, owner actions, or a generic bot registry.
package larkperson

import (
	"context"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

const (
	Kind   = "lark_person"
	Source = "larkcli"
)

var (
	ErrConflict        = errors.New("lark_person client ID already names a different operation")
	ErrUnknown         = errors.New("lark_person delivery outcome is unknown; automatic repost is forbidden")
	ErrUnauthorized    = errors.New("lark_person endpoint or exact source scope is not authorized")
	ErrReadback        = errors.New("lark_person source readback did not prove the frozen delivery")
	ErrNoReply         = errors.New("lark_person found no exact matching reply")
	ErrAmbiguousPerson = errors.New("lark_person directory identity is missing or ambiguous")
	ErrUnsupported     = errors.New("lark_person supports replies in an existing topic only; root sends are unsupported")
	ErrStore           = errors.New("lark_person durable state is unavailable")
	ErrInvalid         = errors.New("lark_person request is invalid")
)

// Endpoint is an external address, not a Persona principal. PersonOpenID is written only by VerifyPerson
// after a server-side directory read; DisplayName is presentation metadata and never identity evidence.
// Revision is also the settings compare-and-swap version.
type Endpoint struct {
	ID               string             `json:"id"`
	Revision         int64              `json:"revision"`
	Kind             string             `json:"kind"`
	DisplayName      string             `json:"display_name"`
	Source           string             `json:"source"`
	Brand            string             `json:"brand"`
	AppID            string             `json:"app_id"`
	AccountOpenID    string             `json:"account_open_id"`
	PersonOpenID     string             `json:"person_open_id,omitempty"`
	PersonVerifiedAt time.Time          `json:"person_verified_at,omitzero"`
	AllowedChatIDs   []string           `json:"allowed_chat_ids,omitempty"`
	Conversation     *ConversationScope `json:"conversation,omitempty"`
	Enabled          bool               `json:"enabled"`
}

// Person is a server-side directory result. OpenID, not Name, is the stable identity.
type Person struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
}

// Topic identifies an existing Lark topic. ChatID and ThreadID are deliberately different opaque namespaces.
type Topic struct {
	ChatID   string `json:"chat_id"`
	ThreadID string `json:"thread_id"`
}

// ConversationScope is an owner-authorized existing-topic binding. HistoryAuthorized is separate from the chat
// allowlist and permits body reads only for this exact chat and thread. ParentMessageID is required for every send.
type ConversationScope struct {
	ChatID            string `json:"chat_id"`
	ThreadID          string `json:"thread_id"`
	ParentMessageID   string `json:"parent_message_id"`
	HistoryAuthorized bool   `json:"history_authorized"`
}

// HistoryRequest authorizes one exact-topic body read at an exact endpoint revision. Cursor pagination is deliberately
// unsupported until a public cursor can be bound without exposing provider tokens; long pages fail closed.
type HistoryRequest struct {
	EndpointID       string
	EndpointRevision int64
	ChatID           string
	ThreadID         string
	Cursor           string
	Limit            int
}

// HistoryPage is scoped source data for the generic Bots projection. It is never persisted as a second transcript.
type HistoryPage struct {
	Messages   []adapter.Message
	HasMore    bool
	NextCursor string
}

// DeliveryRequest is the owner-approved operation. Payload is hashed before persistence and is never logged.
// ParentMessageID must name an existing message in Topic; root delivery is rejected rather than guessing a thread ID.
type DeliveryRequest struct {
	ClientID         string
	TaskID           string
	ParkInstance     string
	EndpointID       string
	EndpointRevision int64
	Payload          string
	ChatID           string
	ThreadID         string
	ParentMessageID  string
}

// DeliveryState distinguishes a durable reservation from a proven source receipt.
type DeliveryState string

const (
	StateReserved DeliveryState = "reserved"
	StateSending  DeliveryState = "sending"
	StateSent     DeliveryState = "sent"
	StateFailed   DeliveryState = "failed"
	StateUnknown  DeliveryState = "unknown"
)

// Reservation is the durable frozen operation. Revision is internal CAS state; EndpointRevision is the approved
// owner-facing endpoint revision. Endpoint is a snapshot, so later edits cannot redirect this delivery.
// The payload itself is not persisted; its exact SHA-256 hash is.
type Reservation struct {
	Revision         int64         `json:"revision"`
	ClientID         string        `json:"client_id"`
	TaskID           string        `json:"task_id"`
	ParkInstance     string        `json:"park_instance"`
	EndpointID       string        `json:"endpoint_id"`
	EndpointRevision int64         `json:"endpoint_revision"`
	Endpoint         Endpoint      `json:"endpoint"`
	PayloadHash      string        `json:"payload_hash"`
	IdempotencyKey   string        `json:"idempotency_key"`
	Topic            Topic         `json:"topic"`
	ParentMessageID  string        `json:"parent_message_id"`
	State            DeliveryState `json:"state"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	RequestMessageID string        `json:"request_message_id,omitempty"`
	RequestURL       string        `json:"request_url,omitempty"`
	SentAt           time.Time     `json:"sent_at,omitzero"`
	ReplyMessageID   string        `json:"reply_message_id,omitempty"`
	Failure          string        `json:"failure,omitempty"`
}

// SentBinding is the exact evidence needed to construct assistant person_reply park evidence.
type SentBinding struct {
	Watch  connectorwire.LarkReplyWatch
	URL    string
	SentAt time.Time
}

// ProactiveRequest authorizes one exact allowlisted connector read. EventID deduplicates transport redelivery only;
// message IDs remain the semantic Inbox/correlation identity.
type ProactiveRequest struct {
	EndpointID       string
	EndpointRevision int64
	ChatID           string
	ThreadID         string
	EventID          string
}

// ReplyReader is satisfied by connector/larkcli.Connector.ObserveReply. It cannot return message bodies.
type ReplyReader interface {
	ObserveReply(context.Context, connectorwire.LarkReplyWatch) (connectorwire.LarkReplyObservation, error)
}

// ReplyRecorder is satisfied by assistant.Service. matched is false for a different task, park instance, or observation.
// Implementations persist pending_review and never deliver the source reply as an Agent instruction or owner consent.
type ReplyRecorder interface {
	RecordLarkPersonReply(context.Context, string, string, connectorwire.LarkReplyObservation) (matched bool, err error)
}
