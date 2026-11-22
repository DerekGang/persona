package extmgr

import (
	"errors"

	larkcliconnector "github.com/nwverse/persona/server/internal/connector/larkcli"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larkperson"
)

var _ larkperson.SettingStore = (*db.DB)(nil)
var _ larkperson.ReplyReader = (*larkcliconnector.Connector)(nil)

// LarkPersonService composes the Feishu-specific adapter for the generic Bots source projection and assistant reply flow.
// Connector credentials stay in the connector; the source receives only this narrow service boundary.
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
