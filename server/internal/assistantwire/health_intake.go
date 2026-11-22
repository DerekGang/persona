package assistantwire

// CapabilityHealthIntake: the manager stores water and protein entries and their undo markers.
// Only the default manager's hello enables the health_intake feature in the hub's hello.
const CapabilityHealthIntake = "health_intake"

func init() {
	Routes = append(Routes,
		"GET /api/v1/assistant/health/intake",
		"POST /api/v1/assistant/health/intake",
		"DELETE /api/v1/assistant/health/intake/{id}",
		"GET /api/v1/assistant/health/intake/changes",
	)
}
