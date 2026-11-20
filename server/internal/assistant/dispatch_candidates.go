package assistant

import (
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
)

var errLauncherInventoryUnavailable = errors.New("launcher inventory unavailable")

// dispatchCandidatesRoutes serves the least-privilege read-only candidates view. It deliberately uses a separate DTO
// so adding fields to an internal launcher or health record cannot leak through this public response.
func (s *Service) dispatchCandidatesRoutes() {
	s.handle("GET /api/v1/assistant/dispatch/candidates", func(w http.ResponseWriter, r *http.Request) {
		machine := r.URL.Query().Get("machine")
		if machine == "" {
			apierr.Write(w, http.StatusNotFound, errUnknownMachine)
			return
		}
		_, rows, err := s.workerPresetRows(r.Context(), machine)
		if errors.Is(err, errUnknownMachine) {
			apierr.Write(w, http.StatusNotFound, errUnknownMachine)
			return
		}
		if err != nil {
			// Keep the public contract literal through the manager's locale-bearing writer too; the generic
			// internal-error localization would otherwise replace error and move this string to detail.
			apierr.WriteBody(w, http.StatusBadGateway, map[string]any{
				"error": errLauncherInventoryUnavailable.Error(), "code": "internal",
			})
			return
		}
		writePresetRows(w, "candidates", toDispatchCandidates(rows))
	})
}

// dispatchCandidate is the explicit public allowlist for GET /api/v1/assistant/dispatch/candidates.
type dispatchCandidate struct {
	Name        string `json:"name"`
	Launcher    string `json:"launcher"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Speed       string `json:"speed,omitempty"`
	Description string `json:"description,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
}

func toDispatchCandidates(rows []workerPreset) []dispatchCandidate {
	out := make([]dispatchCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, dispatchCandidate(row))
	}
	return out
}
