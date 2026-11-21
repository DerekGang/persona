package larkperson

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// CorrelateReply joins delivery -> task/park instance -> exact source observation -> assistant pending_review.
// It never interprets reply text and has no Agent resume, approval, owner-consent, or external-reply capability.
func (s *Service) CorrelateReply(ctx context.Context, clientID string, recorder ReplyRecorder) (connectorwire.LarkReplyObservation, error) {
	var zero connectorwire.LarkReplyObservation
	if s.replies == nil || recorder == nil || !validOpaqueID(clientID) {
		return zero, ErrNoReply
	}
	lock := s.clientLock(clientID)
	lock.Lock()
	defer lock.Unlock()
	reservation, found, err := s.store.reservation(ctx, clientID)
	if err != nil {
		return zero, ErrStore
	}
	if !found || reservation.State != StateSent {
		return zero, ErrUnknown
	}
	binding, err := s.Binding(ctx, clientID)
	if err != nil {
		return zero, err
	}
	observation, err := s.replies.ObserveReply(ctx, binding.Watch)
	if err != nil {
		return zero, ErrNoReply
	}
	if err := validateObservation(reservation, binding, observation, s.clock()); err != nil {
		return zero, err
	}
	if reservation.ReplyMessageID != "" {
		if reservation.ReplyMessageID != observation.Reply.ID {
			return zero, ErrConflict
		}
		return observation, nil
	}
	matched, err := recorder.RecordLarkPersonReply(ctx, reservation.TaskID, reservation.ParkInstance, observation)
	if err != nil || !matched {
		return zero, ErrNoReply
	}
	updated := reservation
	updated.Revision++
	updated.ReplyMessageID = observation.Reply.ID
	updated.UpdatedAt = s.clock()
	if err := s.store.putReservation(ctx, reservation.Revision, updated); err != nil {
		current, _, readErr := s.store.reservation(ctx, clientID)
		if readErr == nil && current.ReplyMessageID == observation.Reply.ID {
			return observation, nil
		}
		return zero, storeError(err)
	}
	return observation, nil
}

func validateObservation(reservation Reservation, binding SentBinding, observation connectorwire.LarkReplyObservation, now time.Time) error {
	if observation.Watch != binding.Watch || observation.CheckedAt.IsZero() || observation.CheckedAt.After(now) ||
		observation.CheckedAt.Before(reservation.CreatedAt) {
		return ErrNoReply
	}
	request, reply := observation.Request, observation.Reply
	if request.ID != binding.Watch.RequestMessageID || request.ChatID != reservation.Topic.ChatID ||
		request.ThreadID != reservation.Topic.ThreadID || !request.At.Equal(reservation.SentAt) ||
		!validReceiptTime(request.At, request.Before, observation.CheckedAt) || request.Before.Add(sourceClockSkew).Before(reservation.CreatedAt) {
		return ErrNoReply
	}
	if !botSender(adapter.Sender{ID: request.SenderID, IDType: request.IDType, SenderType: request.SenderType, AppID: request.AppID}, reservation.Endpoint) {
		return ErrNoReply
	}
	canonical, ok := topicURL(reservation.Endpoint.Brand, reservation.Topic, request.ID, request.URL)
	if !ok || canonical != binding.URL {
		return ErrNoReply
	}
	if !validOpaqueID(reply.ID) || reply.ID == request.ID || reply.SenderType != "user" || reply.IDType != "open_id" ||
		reply.SenderID != reservation.Endpoint.PersonOpenID || reply.SenderID == reservation.Endpoint.AccountOpenID || reply.AppID != "" ||
		reply.ChatID != reservation.Topic.ChatID || reply.ThreadID != reservation.Topic.ThreadID ||
		!validReceiptTime(reply.At, reply.Before, observation.CheckedAt) || reply.At.Before(request.Before) {
		return ErrNoReply
	}
	return nil
}

func validReceiptTime(at, before, checked time.Time) bool {
	return !at.IsZero() && before.After(at) && before.Sub(at) <= time.Minute && !before.After(checked)
}
