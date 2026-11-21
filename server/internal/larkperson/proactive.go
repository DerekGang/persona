package larkperson

import (
	"context"
	"sort"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// ReadProactive performs only the explicitly requested chat/topic read and returns body-free, deterministic FYI things.
// It cannot resume an Agent, grant a permission, approve, deploy, merge, or send an external reply.
func (s *Service) ReadProactive(ctx context.Context, request ProactiveRequest) ([]connectorwire.InboxThing, error) {
	if !validOpaqueID(request.EndpointID) || request.EndpointRevision < 1 || !validOpaqueID(request.ChatID) ||
		!validOpaqueID(request.ThreadID) || request.EventID != "" && !validOpaqueID(request.EventID) {
		return nil, ErrInvalid
	}
	endpoint, err := s.Endpoint(ctx, request.EndpointID)
	if err != nil {
		return nil, err
	}
	if endpoint.Revision != request.EndpointRevision || !endpoint.Enabled || endpoint.PersonOpenID == "" ||
		len(endpoint.AllowedChatIDs) == 0 || !chatAllowed(endpoint, request.ChatID) {
		return nil, ErrUnauthorized
	}
	if err := s.authorizeIdentity(ctx, endpoint); err != nil {
		return nil, err
	}
	page, err := s.transport.ListMessages(ctx, request.ChatID, request.ThreadID, "", 50)
	if err != nil || page.HasMore {
		return nil, ErrReadback
	}
	if request.EventID != "" {
		receipt, found, err := s.store.event(ctx, endpoint.ID, request.EventID)
		if err != nil {
			return nil, ErrStore
		}
		if found {
			for _, message := range page.Messages {
				if proactiveSender(message, endpoint) && message.ChatID == request.ChatID && message.ThreadID == request.ThreadID &&
					message.MessageID != receipt.MessageID {
					return nil, ErrConflict
				}
			}
		}
	}
	seen := map[string]adapter.Message{}
	messages := []adapter.Message{}
	for _, message := range page.Messages {
		if !proactiveSender(message, endpoint) || message.ChatID != request.ChatID || message.ThreadID != request.ThreadID ||
			message.Deleted || message.MsgType != "text" && message.MsgType != "post" || !validOpaqueID(message.MessageID) {
			continue
		}
		at, _, ok := sourceInterval(message.CreateTime, s.clock())
		if !ok {
			return nil, ErrReadback
		}
		if previous, duplicate := seen[message.MessageID]; duplicate {
			if previous.CreateTime != message.CreateTime || previous.ChatID != message.ChatID || previous.ThreadID != message.ThreadID {
				return nil, ErrConflict
			}
			continue
		}
		message.CreateTime = at.Format(time.RFC3339Nano)
		seen[message.MessageID] = message
		messages = append(messages, message)
	}
	if request.EventID != "" && len(messages) > 1 {
		return nil, ErrInvalid
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].MessageID < messages[j].MessageID })
	things := make([]connectorwire.InboxThing, 0, len(messages))
	for _, message := range messages {
		// Claim the transport event before any Inbox write. Competing events with the same ID but different
		// semantic messages therefore cannot each leave an item behind.
		if err := s.rememberProactiveEvent(ctx, endpoint.ID, request.EventID, message.MessageID); err != nil {
			return nil, err
		}
		thing, err := s.proactiveThing(ctx, endpoint, request, message)
		if err != nil {
			return nil, err
		}
		things = append(things, thing)
	}
	return things, nil
}

func proactiveSender(message adapter.Message, endpoint Endpoint) bool {
	return message.Sender.SenderType == "user" && message.Sender.IDType == "open_id" &&
		message.Sender.ID == endpoint.PersonOpenID && message.Sender.ID != endpoint.AccountOpenID && message.Sender.AppID == ""
}

func (s *Service) proactiveThing(ctx context.Context, endpoint Endpoint, request ProactiveRequest, message adapter.Message) (connectorwire.InboxThing, error) {
	at, _, _ := sourceInterval(message.CreateTime, s.clock())
	existing, found, err := s.store.inbox(ctx, endpoint.ID, message.MessageID)
	if err != nil {
		return connectorwire.InboxThing{}, ErrStore
	}
	if found {
		if !existing.Thing.At.Equal(at) || existing.Thing.Last != message.MessageID {
			return connectorwire.InboxThing{}, ErrConflict
		}
		return existing.Thing, nil
	}
	sourceURL, ok := topicURL(endpoint.Brand, Topic{ChatID: request.ChatID, ThreadID: request.ThreadID}, message.MessageID, message.MessageAppLink)
	if !ok {
		return connectorwire.InboxThing{}, ErrReadback
	}
	thing := connectorwire.InboxThing{
		Thread: "lark_person:" + endpoint.ID + ":" + hashText(request.ChatID + "\x00" + request.ThreadID)[:24],
		Kind:   "external_message", Title: endpoint.DisplayName, Sender: endpoint.DisplayName,
		At: at, Last: message.MessageID, URL: sourceURL, Tier: connectorwire.TierFYI,
		// Preview and Context deliberately remain empty: ObserveReply and this scoped identity read do not authorize body display.
	}
	claim, claimFound, err := s.store.topic(ctx, endpoint, Topic{ChatID: request.ChatID, ThreadID: request.ThreadID})
	if err != nil {
		return connectorwire.InboxThing{}, ErrStore
	}
	if claimFound && claim.Active && claim.EndpointID == endpoint.ID {
		reservation, reservationFound, err := s.store.reservation(ctx, claim.ClientID)
		if err != nil {
			return connectorwire.InboxThing{}, ErrStore
		}
		if reservationFound && reservation.State == StateSent && reservation.EndpointID == endpoint.ID &&
			reservation.EndpointRevision == endpoint.Revision && reservation.Topic.ChatID == request.ChatID &&
			reservation.Topic.ThreadID == request.ThreadID {
			thing.Parent = &connectorwire.ThingParent{Kind: "todo", ID: reservation.TaskID}
		}
	}
	receipt := inboxReceipt{Revision: 1, EventID: request.EventID, Thing: thing}
	if err := s.store.putInbox(ctx, 0, endpoint.ID, message.MessageID, receipt); err != nil {
		if err == errCompareConflict {
			current, _, readErr := s.store.inbox(ctx, endpoint.ID, message.MessageID)
			if readErr == nil && current.Thing.Last == message.MessageID && current.Thing.At.Equal(at) {
				return current.Thing, nil
			}
		}
		return connectorwire.InboxThing{}, storeError(err)
	}
	return thing, nil
}

func (s *Service) rememberProactiveEvent(ctx context.Context, endpointID, eventID, messageID string) error {
	if eventID == "" {
		return nil
	}
	receipt, found, err := s.store.event(ctx, endpointID, eventID)
	if err != nil {
		return ErrStore
	}
	if found {
		if receipt.MessageID != messageID {
			return ErrConflict
		}
		return nil
	}
	receipt = eventReceipt{Revision: 1, EventID: eventID, MessageID: messageID}
	if err := s.store.putEvent(ctx, 0, endpointID, receipt); err != nil {
		if err == errCompareConflict {
			current, _, readErr := s.store.event(ctx, endpointID, eventID)
			if readErr == nil && current.MessageID == messageID {
				return nil
			}
		}
		return storeError(err)
	}
	return nil
}
