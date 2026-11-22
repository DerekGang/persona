package larkperson

import (
	"context"
	"encoding/json"
	"sort"
)

// Endpoints lists only this adapter's owner-managed records for the later generic Bots registry projection.
// It is not a second generic endpoint registry and includes no credentials or Persona grants.
func (s *Service) Endpoints(ctx context.Context) ([]Endpoint, error) {
	values, err := s.store.settings.SettingsPrefix(ctx, endpointPrefix)
	if err != nil {
		return nil, ErrStore
	}
	endpoints := make([]Endpoint, 0, len(values))
	for _, raw := range values {
		var endpoint Endpoint
		if json.Unmarshal(raw, &endpoint) != nil || endpoint.ID == "" {
			return nil, ErrStore
		}
		endpoints = append(endpoints, cloneEndpoint(endpoint))
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].ID < endpoints[j].ID })
	return endpoints, nil
}
