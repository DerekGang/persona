package extmgr

import (
	"errors"

	larkcliconnector "github.com/nwverse/persona/server/internal/connector/larkcli"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larkperson"
)

var _ larkperson.SettingStore = (*db.DB)(nil)
var _ larkperson.ReplyReader = (*larkcliconnector.Connector)(nil)

// LarkPersonService composes the Feishu-specific adapter without registering a generic Bots source or adding hub routes.
// The Orca-owned generic registry/UI can consume this manager boundary during the later integration step.
func (h *Manager) LarkPersonService() (*larkperson.Service, error) {
	if h == nil || h.connectors == nil || !h.connectors.Enabled(larkcliconnector.ID) {
		return nil, errors.New("Lark CLI connector is unavailable")
	}
	connector, ok := h.connectors.Get(larkcliconnector.ID).(*larkcliconnector.Connector)
	if !ok || connector == nil {
		return nil, errors.New("Lark CLI connector is unavailable")
	}
	store, ok := h.store.(larkperson.SettingStore)
	if !ok || store == nil {
		return nil, errors.New("lark_person durable settings store is unavailable")
	}
	return larkperson.New(store, connector.PersonTransport(), connector, nil)
}
