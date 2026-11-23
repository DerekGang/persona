package extwire

// The attention mode (#13503, assistantwire/attention.go) is the attention domain's decision, made here rather than in
// scopes.go: an extension holding notify may read the mode in force, as it reads the low-distraction switch
// (GET /api/v1/push/ln-mode). Setting it stays full credentials only: only the user changes it, in an app.
func init() {
	addRouteScopes(map[string]string{
		"GET /api/v1/assistant/attention": ScopeNotify,
	})
}
