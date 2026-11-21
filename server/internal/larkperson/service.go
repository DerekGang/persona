package larkperson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// Service owns lark_person business logic. It has no HTTP, owner-actor, channel, BotMux, or Agent dependencies.
type Service struct {
	store     settingsStore
	transport Transport
	replies   ReplyReader
	now       func() time.Time
	locks     sync.Map
}

func New(store SettingStore, transport Transport, replies ReplyReader, now func() time.Time) (*Service, error) {
	if store == nil || transport == nil {
		return nil, ErrStore
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: settingsStore{settings: store}, transport: transport, replies: replies, now: now}, nil
}

func (s *Service) clock() time.Time { return s.now().UTC() }

func (s *Service) clientLock(id string) *sync.Mutex {
	lock, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *Service) CreateEndpoint(ctx context.Context, input Endpoint) (Endpoint, error) {
	input.Revision = 1
	input.PersonOpenID = ""
	input.PersonVerifiedAt = time.Time{}
	if err := validateEndpoint(input, false); err != nil {
		return Endpoint{}, err
	}
	input.AllowedChatIDs = cleanIDs(input.AllowedChatIDs)
	if err := s.store.putEndpoint(ctx, 0, input); err != nil {
		return Endpoint{}, storeError(err)
	}
	return cloneEndpoint(input), nil
}

func (s *Service) Endpoint(ctx context.Context, id string) (Endpoint, error) {
	if !validOpaqueID(id) {
		return Endpoint{}, ErrInvalid
	}
	endpoint, err := s.store.endpoint(ctx, id)
	if err != nil {
		return Endpoint{}, ErrStore
	}
	if endpoint.ID == "" {
		return Endpoint{}, ErrUnauthorized
	}
	return cloneEndpoint(endpoint), nil
}

func (s *Service) UpdateEndpoint(ctx context.Context, input Endpoint) (Endpoint, error) {
	current, err := s.Endpoint(ctx, input.ID)
	if err != nil {
		return Endpoint{}, err
	}
	if input.Revision != current.Revision {
		return Endpoint{}, ErrConflict
	}
	sameIdentity := current.Source == input.Source && current.Brand == input.Brand && current.AppID == input.AppID &&
		current.AccountOpenID == input.AccountOpenID
	if sameIdentity {
		input.PersonOpenID = current.PersonOpenID
		input.PersonVerifiedAt = current.PersonVerifiedAt
	} else {
		input.PersonOpenID = ""
		input.PersonVerifiedAt = time.Time{}
	}
	input.Revision++
	if err := validateEndpoint(input, input.PersonOpenID != ""); err != nil {
		return Endpoint{}, err
	}
	input.AllowedChatIDs = cleanIDs(input.AllowedChatIDs)
	if err := s.store.putEndpoint(ctx, current.Revision, input); err != nil {
		return Endpoint{}, storeError(err)
	}
	return cloneEndpoint(input), nil
}

// VerifyPerson resolves identity through the configured server-side directory. A display name can narrow the query,
// but only one unambiguous directory OpenID selected by the owner can be saved.
func (s *Service) VerifyPerson(ctx context.Context, endpointID, query, selectedOpenID string) (Endpoint, error) {
	current, err := s.Endpoint(ctx, endpointID)
	if err != nil {
		return Endpoint{}, err
	}
	query, selectedOpenID = strings.TrimSpace(query), strings.TrimSpace(selectedOpenID)
	if query == "" || utf8.RuneCountInString(query) > 200 || !validOpaqueID(selectedOpenID) {
		return Endpoint{}, ErrInvalid
	}
	if err := s.authorizeIdentity(ctx, current); err != nil {
		return Endpoint{}, err
	}
	people, err := s.transport.SearchPeople(ctx, query, 30)
	if err != nil || len(people) != 1 || people[0].OpenID != selectedOpenID || people[0].Name != query {
		return Endpoint{}, ErrAmbiguousPerson
	}
	current.PersonOpenID = selectedOpenID
	current.PersonVerifiedAt = s.clock()
	current.Revision++
	if err := validateEndpoint(current, true); err != nil {
		return Endpoint{}, err
	}
	if err := s.store.putEndpoint(ctx, current.Revision-1, current); err != nil {
		return Endpoint{}, storeError(err)
	}
	return cloneEndpoint(current), nil
}

func validateEndpoint(endpoint Endpoint, verified bool) error {
	if !validOpaqueID(endpoint.ID) || endpoint.Revision < 1 || endpoint.Kind != Kind || endpoint.Source != Source ||
		(endpoint.Brand != "feishu" && endpoint.Brand != "lark") || !validOpaqueID(endpoint.AppID) ||
		!validOpaqueID(endpoint.AccountOpenID) || endpoint.AccountOpenID == endpoint.PersonOpenID {
		return ErrInvalid
	}
	name := strings.TrimSpace(endpoint.DisplayName)
	if name == "" || name != endpoint.DisplayName || utf8.RuneCountInString(name) > 200 {
		return ErrInvalid
	}
	if verified {
		if !validOpaqueID(endpoint.PersonOpenID) || endpoint.PersonVerifiedAt.IsZero() {
			return ErrInvalid
		}
	} else if endpoint.PersonOpenID != "" || !endpoint.PersonVerifiedAt.IsZero() {
		return ErrInvalid
	}
	ids := cleanIDs(endpoint.AllowedChatIDs)
	if len(ids) != len(endpoint.AllowedChatIDs) {
		return ErrInvalid
	}
	for _, id := range ids {
		if !validOpaqueID(id) || id == endpoint.AccountOpenID || id == endpoint.PersonOpenID {
			return ErrInvalid
		}
	}
	return nil
}

func cleanIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return []string{""}
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func cloneEndpoint(endpoint Endpoint) Endpoint {
	endpoint.AllowedChatIDs = append([]string(nil), endpoint.AllowedChatIDs...)
	return endpoint
}

func validOpaqueID(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validInstance(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 200 && !strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (s *Service) Deliver(ctx context.Context, request DeliveryRequest) (Reservation, error) {
	if err := validateDelivery(request); err != nil {
		return Reservation{}, err
	}
	lock := s.clientLock(request.ClientID)
	lock.Lock()
	defer lock.Unlock()

	hash := sha256.Sum256([]byte(request.Payload))
	payloadHash := hex.EncodeToString(hash[:])
	reservation, found, err := s.store.reservation(ctx, request.ClientID)
	if err != nil {
		return Reservation{}, ErrStore
	}
	if !found {
		endpoint, err := s.Endpoint(ctx, request.EndpointID)
		if err != nil {
			return Reservation{}, err
		}
		if endpoint.Revision != request.EndpointRevision || !endpoint.Enabled || endpoint.PersonOpenID == "" ||
			!chatAllowed(endpoint, request.ChatID) {
			return Reservation{}, ErrUnauthorized
		}
		now := s.clock()
		reservation = Reservation{
			Revision: 1, ClientID: request.ClientID, TaskID: request.TaskID, ParkInstance: request.ParkInstance,
			EndpointID: request.EndpointID, EndpointRevision: request.EndpointRevision, Endpoint: cloneEndpoint(endpoint),
			PayloadHash: payloadHash, IdempotencyKey: operationKey(request, payloadHash),
			Topic: Topic{ChatID: request.ChatID, ThreadID: request.ThreadID}, ParentMessageID: request.ParentMessageID,
			State: StateReserved, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.putReservation(ctx, 0, reservation); err != nil {
			if !errors.Is(err, errCompareConflict) {
				return Reservation{}, ErrStore
			}
			// Another manager reserved the same client ID first. Reload it and apply the ordinary idempotency checks
			// instead of turning an identical concurrent submission into a spurious conflict.
			reservation, found, err = s.store.reservation(ctx, request.ClientID)
			if err != nil {
				return Reservation{}, ErrStore
			}
			if !found {
				return Reservation{}, ErrConflict
			}
		}
	}
	if found {
		if !sameOperation(reservation, request, payloadHash) {
			return reservation, ErrConflict
		}
		switch reservation.State {
		case StateSent:
			return reservation, nil
		case StateUnknown:
			return reservation, ErrUnknown
		case StateSending:
			return s.markRestartUnknown(ctx, reservation)
		case StateReserved, StateFailed:
			// Only these two states may enter transport, with the already frozen operation and key.
		default:
			return reservation, ErrUnknown
		}
	}
	if err := s.claimTopic(ctx, &reservation); err != nil {
		return reservation, err
	}

	sending := reservation
	sending.Revision++
	sending.State = StateSending
	sending.UpdatedAt = s.clock()
	sending.Failure = ""
	if err := s.store.putReservation(ctx, reservation.Revision, sending); err != nil {
		if errors.Is(err, errCompareConflict) {
			current, _, readErr := s.store.reservation(ctx, reservation.ClientID)
			if readErr == nil && sameOperation(current, request, payloadHash) {
				switch current.State {
				case StateSent:
					return current, nil
				case StateSending, StateUnknown:
					return current, ErrUnknown
				}
			}
		}
		return reservation, storeError(err)
	}
	reservation = sending

	if err := s.authorizeCurrent(ctx, reservation.Endpoint, reservation.Topic.ChatID); err != nil {
		return s.finishKnownFailure(ctx, reservation, "authorization")
	}
	if err := s.verifyParent(ctx, reservation); err != nil {
		return s.finishKnownFailure(ctx, reservation, "parent_scope")
	}
	raw, err := s.transport.ReplyText(ctx, reservation.ParentMessageID, request.Payload, reservation.IdempotencyKey, true)
	if err != nil {
		if knownNonDelivery(err) {
			return s.finishKnownFailure(ctx, reservation, string(adapter.KindOf(err)))
		}
		return s.finishUnknown(ctx, reservation)
	}
	requestID := responseMessageID(raw)
	if requestID == "" {
		return s.finishUnknown(ctx, reservation)
	}
	requestMessage, err := s.readback(ctx, reservation, requestID)
	if err != nil {
		return s.finishUnknown(ctx, reservation)
	}
	sent := reservation
	sent.Revision++
	sent.State = StateSent
	sent.UpdatedAt = s.clock()
	sent.RequestMessageID = requestID
	sent.RequestURL = requestMessage.URL
	sent.SentAt = requestMessage.At
	sent.Failure = ""
	if err := s.store.putReservation(ctx, reservation.Revision, sent); err != nil {
		current, _, readErr := s.store.reservation(ctx, reservation.ClientID)
		if readErr == nil && current.State == StateSent {
			return current, nil
		}
		// A concurrent duplicate (another Service instance: the manager builds one per call) found this delivery
		// "sending" and marked it unknown while it was in flight. The readback above proves it, so record it as sent
		// rather than leaving a delivered message unknown, with no binding for its reply.
		if readErr == nil && current.State == StateUnknown && current.RequestMessageID == "" && sameOperation(current, request, payloadHash) {
			sent.Revision = current.Revision + 1
			if s.store.putReservation(ctx, current.Revision, sent) == nil {
				return sent, nil
			}
		}
		return reservation, ErrUnknown
	}
	return sent, nil
}

func validateDelivery(request DeliveryRequest) error {
	if request.ThreadID == "" && validOpaqueID(request.ChatID) && validOpaqueID(request.ParentMessageID) {
		return ErrUnsupported
	}
	if !validOpaqueID(request.ClientID) || !validOpaqueID(request.TaskID) || !validInstance(request.ParkInstance) ||
		!validOpaqueID(request.EndpointID) || request.EndpointRevision < 1 || !validOpaqueID(request.ChatID) ||
		!validOpaqueID(request.ThreadID) || !validOpaqueID(request.ParentMessageID) {
		return ErrInvalid
	}
	if request.Payload == "" || !utf8.ValidString(request.Payload) || len(request.Payload) > 1<<20 {
		return ErrInvalid
	}
	return nil
}

func sameOperation(reservation Reservation, request DeliveryRequest, payloadHash string) bool {
	return reservation.ClientID == request.ClientID && reservation.TaskID == request.TaskID &&
		reservation.ParkInstance == request.ParkInstance && reservation.EndpointID == request.EndpointID &&
		reservation.EndpointRevision == request.EndpointRevision && reservation.PayloadHash == payloadHash &&
		reservation.Topic.ChatID == request.ChatID && reservation.Topic.ThreadID == request.ThreadID &&
		reservation.ParentMessageID == request.ParentMessageID && reservation.IdempotencyKey != ""
}

func operationKey(request DeliveryRequest, payloadHash string) string {
	return "lp1-" + hashText(strings.Join([]string{request.ClientID, request.TaskID, request.ParkInstance, request.EndpointID,
		strconv.FormatInt(request.EndpointRevision, 10), payloadHash, request.ChatID, request.ThreadID, request.ParentMessageID}, "\x00"))[:48]
}

// claimTopic keeps one active task per exact topic. The same task may take the claim over for a later delivery (a
// follow-up after a reply), and a claim whose delivery is known not to have happened (failed) or whose reservation is
// gone no longer blocks. Any other active claim, including one whose outcome is unknown, refuses the delivery.
func (s *Service) claimTopic(ctx context.Context, reservation *Reservation) error {
	claim, found, err := s.store.topic(ctx, reservation.Endpoint, reservation.Topic)
	if err != nil {
		return ErrStore
	}
	if found && claim.Active {
		if claim.ClientID == reservation.ClientID {
			return nil
		}
		holder, held, err := s.store.reservation(ctx, claim.ClientID)
		if err != nil {
			return ErrStore
		}
		if held && holder.TaskID != reservation.TaskID && holder.State != StateFailed {
			return ErrConflict
		}
	}
	previous := int64(0)
	if found {
		previous = claim.Revision
	}
	next := topicClaim{Revision: previous + 1, ClientID: reservation.ClientID, TaskID: reservation.TaskID,
		EndpointID: reservation.EndpointID, Active: true}
	if err := s.store.putTopic(ctx, previous, reservation.Endpoint, reservation.Topic, next); err != nil {
		if errors.Is(err, errCompareConflict) {
			current, _, readErr := s.store.topic(ctx, reservation.Endpoint, reservation.Topic)
			if readErr == nil && current.Active && current.ClientID == reservation.ClientID {
				return nil
			}
		}
		return storeError(err)
	}
	return nil
}

// ReleaseTopic ends a delivery's claim on its topic, for the integration to call when the delivery's task finishes, so
// another task may use the topic. Only the claim's own delivery releases it; a claim already taken over is left alone.
func (s *Service) ReleaseTopic(ctx context.Context, clientID string) error {
	if !validOpaqueID(clientID) {
		return ErrInvalid
	}
	lock := s.clientLock(clientID)
	lock.Lock()
	defer lock.Unlock()
	reservation, found, err := s.store.reservation(ctx, clientID)
	if err != nil {
		return ErrStore
	}
	if !found {
		return ErrInvalid
	}
	claim, found, err := s.store.topic(ctx, reservation.Endpoint, reservation.Topic)
	if err != nil {
		return ErrStore
	}
	if !found || !claim.Active || claim.ClientID != clientID {
		return nil
	}
	released := claim
	released.Revision++
	released.Active = false
	if err := s.store.putTopic(ctx, claim.Revision, reservation.Endpoint, reservation.Topic, released); err != nil {
		return storeError(err)
	}
	return nil
}

func (s *Service) authorizeIdentity(ctx context.Context, endpoint Endpoint) error {
	if endpoint.Source != Source || endpoint.PersonOpenID != "" && !validOpaqueID(endpoint.PersonOpenID) {
		return ErrUnauthorized
	}
	auth, err := s.transport.AuthStatus(ctx)
	if err != nil || !auth.LoggedIn || auth.Expired || auth.Brand != endpoint.Brand || auth.AppID != endpoint.AppID ||
		auth.OpenID != endpoint.AccountOpenID || auth.OpenID == endpoint.PersonOpenID {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) authorizeCurrent(ctx context.Context, frozen Endpoint, chatID string) error {
	if !frozen.Enabled || frozen.PersonOpenID == "" || !chatAllowed(frozen, chatID) {
		return ErrUnauthorized
	}
	if err := s.authorizeIdentity(ctx, frozen); err != nil {
		return err
	}
	current, err := s.store.endpoint(ctx, frozen.ID)
	if err != nil {
		return ErrStore
	}
	if !current.Enabled || current.Source != frozen.Source || current.Brand != frozen.Brand || current.AppID != frozen.AppID ||
		current.AccountOpenID != frozen.AccountOpenID || current.PersonOpenID != frozen.PersonOpenID ||
		current.PersonVerifiedAt.IsZero() || !chatAllowed(current, chatID) {
		return ErrUnauthorized
	}
	return nil
}

func chatAllowed(endpoint Endpoint, chatID string) bool {
	if len(endpoint.AllowedChatIDs) == 0 {
		return true
	}
	for _, allowed := range endpoint.AllowedChatIDs {
		if allowed == chatID {
			return true
		}
	}
	return false
}

func (s *Service) verifyParent(ctx context.Context, reservation Reservation) error {
	page, err := s.transport.ListMessages(ctx, reservation.Topic.ChatID, reservation.Topic.ThreadID, "", 50)
	if err != nil || page.HasMore {
		return ErrUnauthorized
	}
	for _, message := range page.Messages {
		if message.MessageID == reservation.ParentMessageID && !message.Deleted &&
			(message.MsgType == "text" || message.MsgType == "post") && messageInScope(message, reservation.Topic, reservation.Endpoint.Brand) {
			return nil
		}
	}
	return ErrUnauthorized
}

func (s *Service) readback(ctx context.Context, reservation Reservation, requestID string) (connectorwire.LarkReplyMessage, error) {
	if err := s.authorizeIdentity(ctx, reservation.Endpoint); err != nil {
		return connectorwire.LarkReplyMessage{}, ErrReadback
	}
	page, err := s.transport.ListMessages(ctx, reservation.Topic.ChatID, reservation.Topic.ThreadID, "", 50)
	if err != nil || page.HasMore {
		return connectorwire.LarkReplyMessage{}, ErrReadback
	}
	now := s.clock()
	for _, message := range page.Messages {
		if message.MessageID != requestID || message.Deleted || message.MsgType != "text" ||
			!messageInScope(message, reservation.Topic, reservation.Endpoint.Brand) || !botSender(message.Sender, reservation.Endpoint) {
			continue
		}
		at, before, ok := sourceInterval(message.CreateTime, now)
		if !ok || before.Add(sourceClockSkew).Before(reservation.CreatedAt) {
			return connectorwire.LarkReplyMessage{}, ErrReadback
		}
		sourceURL, ok := topicURL(reservation.Endpoint.Brand, reservation.Topic, requestID, message.MessageAppLink)
		if !ok {
			return connectorwire.LarkReplyMessage{}, ErrReadback
		}
		return connectorwire.LarkReplyMessage{ID: requestID, SenderID: message.Sender.ID, SenderType: message.Sender.SenderType,
			IDType: message.Sender.IDType, AppID: reservation.Endpoint.AppID, ChatID: reservation.Topic.ChatID,
			ThreadID: reservation.Topic.ThreadID, URL: sourceURL, At: at, Before: before}, nil
	}
	return connectorwire.LarkReplyMessage{}, ErrReadback
}

func botSender(sender adapter.Sender, endpoint Endpoint) bool {
	return sender.SenderType == "app" && validOpaqueID(sender.ID) &&
		(sender.IDType == "app_id" && sender.ID == endpoint.AppID || validOpaqueID(sender.AppID) && sender.AppID == endpoint.AppID)
}

func messageInScope(message adapter.Message, topic Topic, brand string) bool {
	if message.ChatID != topic.ChatID || message.ThreadID != topic.ThreadID {
		return false
	}
	if message.MessageAppLink == "" {
		return true
	}
	_, ok := topicURL(brand, topic, message.MessageID, message.MessageAppLink)
	return ok
}

func topicURL(brand string, topic Topic, messageID, raw string) (string, bool) {
	host := "applink.feishu.cn"
	if brand == "lark" {
		host = "applink.larksuite.com"
	} else if brand != "feishu" {
		return "", false
	}
	if raw == "" {
		query := url.Values{"open_chat_id": {topic.ChatID}, "open_thread_id": {topic.ThreadID}, "message_id": {messageID}}
		return "https://" + host + "/client/thread/open?" + query.Encode(), true
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != host || parsed.User != nil || parsed.Fragment != "" ||
		parsed.Path != "/client/thread/open" {
		return "", false
	}
	query := parsed.Query()
	if len(query["open_chat_id"]) != 1 || query.Get("open_chat_id") != topic.ChatID ||
		len(query["open_thread_id"]) != 1 || query.Get("open_thread_id") != topic.ThreadID {
		return "", false
	}
	if ids, present := query["message_id"]; present && (len(ids) != 1 || ids[0] != messageID) {
		return "", false
	}
	query.Set("message_id", messageID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

const sourceClockSkew = 5 * time.Second

func sourceInterval(raw string, now time.Time) (time.Time, time.Time, bool) {
	raw = strings.TrimSpace(raw)
	var at time.Time
	width := time.Duration(0)
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		at = parsed.UTC()
		width = time.Second
		if index := strings.IndexByte(raw, '.'); index >= 0 {
			digits := 0
			for i := index + 1; i < len(raw) && raw[i] >= '0' && raw[i] <= '9'; i++ {
				digits++
			}
			width = time.Nanosecond
			for i := digits; i < 9; i++ {
				width *= 10
			}
		}
	} else if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
		switch len(raw) {
		case 10:
			at, width = time.Unix(value, 0).UTC(), time.Second
		case 13:
			at, width = time.UnixMilli(value).UTC(), time.Millisecond
		default:
			return time.Time{}, time.Time{}, false
		}
	} else {
		return time.Time{}, time.Time{}, false
	}
	before := at.Add(width)
	if at.IsZero() || width <= 0 || width > time.Minute || !before.After(at) || before.After(now.UTC()) {
		return time.Time{}, time.Time{}, false
	}
	return at, before, true
}

func knownNonDelivery(err error) bool {
	switch adapter.KindOf(err) {
	case adapter.KindNotInstalled, adapter.KindNotLoggedIn, adapter.KindExpired, adapter.KindMissingPermission, adapter.KindUnsupportedVersion:
		return true
	default:
		return false
	}
}

func (s *Service) finishKnownFailure(ctx context.Context, reservation Reservation, code string) (Reservation, error) {
	failed := reservation
	failed.Revision++
	failed.State = StateFailed
	failed.UpdatedAt = s.clock()
	failed.Failure = code
	if err := s.store.putReservation(ctx, reservation.Revision, failed); err != nil {
		return reservation, ErrStore
	}
	return failed, ErrUnauthorized
}

func (s *Service) finishUnknown(ctx context.Context, reservation Reservation) (Reservation, error) {
	unknown := reservation
	unknown.Revision++
	unknown.State = StateUnknown
	unknown.UpdatedAt = s.clock()
	unknown.Failure = "unverified_transport"
	if err := s.store.putReservation(ctx, reservation.Revision, unknown); err != nil {
		return reservation, ErrUnknown
	}
	return unknown, ErrUnknown
}

func (s *Service) markRestartUnknown(ctx context.Context, reservation Reservation) (Reservation, error) {
	return s.finishUnknown(ctx, reservation)
}

func storeError(err error) error {
	if errors.Is(err, errCompareConflict) {
		return ErrConflict
	}
	return ErrStore
}

// Binding returns evidence only for a sent, readback-verified reservation.
func (s *Service) Binding(ctx context.Context, clientID string) (SentBinding, error) {
	reservation, found, err := s.store.reservation(ctx, clientID)
	if err != nil {
		return SentBinding{}, ErrStore
	}
	if !found || reservation.State != StateSent || reservation.RequestMessageID == "" || reservation.SentAt.IsZero() {
		return SentBinding{}, ErrUnknown
	}
	return SentBinding{Watch: connectorwire.LarkReplyWatch{Source: Source, Brand: reservation.Endpoint.Brand,
		AppID: reservation.Endpoint.AppID, AccountOpenID: reservation.Endpoint.AccountOpenID, PersonOpenID: reservation.Endpoint.PersonOpenID,
		ChatID: reservation.Topic.ChatID, ThreadID: reservation.Topic.ThreadID, RequestMessageID: reservation.RequestMessageID},
		URL: reservation.RequestURL, SentAt: reservation.SentAt}, nil
}

// Reservation returns a copy of the durable operation for owner-facing status and generic Bots integration.
func (s *Service) Reservation(ctx context.Context, clientID string) (Reservation, error) {
	reservation, found, err := s.store.reservation(ctx, clientID)
	if err != nil {
		return Reservation{}, ErrStore
	}
	if !found {
		return Reservation{}, ErrInvalid
	}
	reservation.Endpoint = cloneEndpoint(reservation.Endpoint)
	return reservation, nil
}
