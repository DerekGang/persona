package larkperson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/nwverse/persona/server/internal/connectorwire"
)

const (
	settingsPrefix  = "lark_person."
	endpointPrefix  = settingsPrefix + "endpoint."
	deliveryPrefix  = settingsPrefix + "delivery."
	topicPrefix     = settingsPrefix + "topic."
	eventPrefix     = settingsPrefix + "event."
	inboxPrefix     = settingsPrefix + "inbox."
	correlationSuff = ".correlation"
)

var errCompareConflict = errors.New("settings compare conflict")

// SettingStore is the existing manager Postgres settings surface. CompareSetting and SettingsPrefix are implemented
// by internal/db.DB. Keeping this interface small avoids changing extmgr.Store and all of its test doubles.
type SettingStore interface {
	Setting(context.Context, string, any) (bool, error)
	PutSetting(context.Context, string, any) error
	CompareSetting(context.Context, string, int64, any) (bool, error)
	SettingsPrefix(context.Context, string) (map[string]json.RawMessage, error)
}

type settingsStore struct {
	settings SettingStore
}

type topicClaim struct {
	Revision   int64  `json:"revision"`
	ClientID   string `json:"client_id"`
	EndpointID string `json:"endpoint_id"`
	Active     bool   `json:"active"`
}

type eventReceipt struct {
	Revision  int64  `json:"revision"`
	EventID   string `json:"event_id"`
	MessageID string `json:"message_id"`
}

type inboxReceipt struct {
	Revision int64                    `json:"revision"`
	EventID  string                   `json:"event_id"`
	Thing    connectorwire.InboxThing `json:"thing"`
}

func endpointKey(id string) string { return endpointPrefix + id }
func deliveryKey(id string) string { return deliveryPrefix + id }
func eventKey(id string) string    { return eventPrefix + hashText(id) }
func inboxKey(id string) string    { return inboxPrefix + hashText(id) }
func topicKey(id string) string    { return topicPrefix + hashText(id) }

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func topicIdentity(endpoint Endpoint, topic Topic) string {
	return endpoint.Source + "\x00" + endpoint.Brand + "\x00" + endpoint.AppID + "\x00" + topic.ChatID + "\x00" + topic.ThreadID
}

func (s *settingsStore) endpoint(ctx context.Context, id string) (Endpoint, error) {
	var endpoint Endpoint
	found, err := s.settings.Setting(ctx, endpointKey(id), &endpoint)
	if err != nil || !found {
		return Endpoint{}, err
	}
	return endpoint, nil
}

func (s *settingsStore) putEndpoint(ctx context.Context, previous int64, endpoint Endpoint) error {
	return s.compare(ctx, endpointKey(endpoint.ID), previous, endpoint)
}

func (s *settingsStore) reservation(ctx context.Context, id string) (Reservation, bool, error) {
	var reservation Reservation
	found, err := s.settings.Setting(ctx, deliveryKey(id), &reservation)
	return reservation, found, err
}

func (s *settingsStore) putReservation(ctx context.Context, previous int64, reservation Reservation) error {
	return s.compare(ctx, deliveryKey(reservation.ClientID), previous, reservation)
}

func (s *settingsStore) topic(ctx context.Context, endpoint Endpoint, topic Topic) (topicClaim, bool, error) {
	var claim topicClaim
	found, err := s.settings.Setting(ctx, topicKey(topicIdentity(endpoint, topic)), &claim)
	return claim, found, err
}

func (s *settingsStore) putTopic(ctx context.Context, previous int64, endpoint Endpoint, topic Topic, claim topicClaim) error {
	return s.compare(ctx, topicKey(topicIdentity(endpoint, topic)), previous, claim)
}

func (s *settingsStore) event(ctx context.Context, endpointID, eventID string) (eventReceipt, bool, error) {
	var receipt eventReceipt
	found, err := s.settings.Setting(ctx, eventKey(endpointID+"\x00"+eventID), &receipt)
	return receipt, found, err
}

func (s *settingsStore) putEvent(ctx context.Context, previous int64, endpointID string, receipt eventReceipt) error {
	return s.compare(ctx, eventKey(endpointID+"\x00"+receipt.EventID), previous, receipt)
}

func (s *settingsStore) inbox(ctx context.Context, endpointID, messageID string) (inboxReceipt, bool, error) {
	var receipt inboxReceipt
	found, err := s.settings.Setting(ctx, inboxKey(endpointID+"\x00"+messageID), &receipt)
	return receipt, found, err
}

func (s *settingsStore) putInbox(ctx context.Context, previous int64, endpointID, messageID string, receipt inboxReceipt) error {
	return s.compare(ctx, inboxKey(endpointID+"\x00"+messageID), previous, receipt)
}

func (s *settingsStore) compare(ctx context.Context, key string, previous int64, value any) error {
	ok, err := s.settings.CompareSetting(ctx, key, previous, value)
	if err != nil {
		return err
	}
	if !ok {
		return errCompareConflict
	}
	return nil
}
