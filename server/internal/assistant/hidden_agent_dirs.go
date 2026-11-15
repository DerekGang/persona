package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const hiddenAgentDirsKey = "agents.hidden_dirs"

type hiddenAgentDir struct {
	MachineID string `json:"machine_id"`
	Pattern   string `json:"pattern"`
	Enabled   bool   `json:"enabled"`
}
type hiddenAgentDirs struct {
	Rules []hiddenAgentDir `json:"rules"`
}
type hiddenDirBook struct {
	mu     sync.Mutex
	loaded bool
	at     time.Time
	value  hiddenAgentDirs
}

func (s *Service) readHiddenDirs(ctx context.Context) (hiddenAgentDirs, error) {
	b := &s.hiddenDirs
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loaded || time.Since(b.at) > 5*time.Second {
		v := hiddenAgentDirs{Rules: []hiddenAgentDir{}}
		if st := s.store(); st != nil {
			if _, err := st.Setting(ctx, hiddenAgentDirsKey, &v); err != nil {
				return v, err
			}
		}
		b.value, b.loaded, b.at = v, true, time.Now()
	}
	v := b.value
	v.Rules = slices.Clone(v.Rules)
	if v.Rules == nil {
		v.Rules = []hiddenAgentDir{}
	}
	return v, nil
}

// Paths are interpreted on the named computer, never on the manager's host.
// A literal or glob matches a directory and its descendants; ** spans components.
func directoryMatches(cwd, home, pattern string) bool {
	normalize := func(v string) string { return strings.ReplaceAll(v, "\\", "/") }
	expand := func(v string) string {
		v = normalize(v)
		if v == "~" || strings.HasPrefix(v, "~/") {
			if home == "" {
				return ""
			}
			v = strings.TrimRight(normalize(home), "/") + strings.TrimPrefix(v, "~")
		}
		if v == "" {
			return ""
		}
		return path.Clean(v)
	}
	cwd, pattern = expand(cwd), expand(pattern)
	if cwd == "" || pattern == "" {
		return false
	}
	var memo map[[2]int]bool
	var visited map[[2]int]bool
	var matches func([]string, []string) bool
	matches = func(p, d []string) (result bool) {
		key := [2]int{len(p), len(d)}
		if visited[key] {
			return memo[key]
		}
		visited[key] = true
		defer func() { memo[key] = result }()
		if len(p) == 0 {
			return len(d) == 0
		}
		if p[0] == "**" {
			return matches(p[1:], d) || len(d) > 0 && matches(p, d[1:])
		}
		if len(d) == 0 {
			return false
		}
		ok, _ := path.Match(p[0], d[0])
		return ok && matches(p[1:], d[1:])
	}
	for {
		memo, visited = map[[2]int]bool{}, map[[2]int]bool{}
		if matches(strings.Split(pattern, "/"), strings.Split(cwd, "/")) {
			return true
		}
		parent := path.Dir(cwd)
		if parent == cwd || parent == "." {
			return false
		}
		cwd = parent
	}
}

func validateHiddenDirs(v *hiddenAgentDirs) error {
	if len(v.Rules) > 100 {
		return errors.New("at most 100 directory rules")
	}
	seen := map[[2]string]bool{}
	for i := range v.Rules {
		r := &v.Rules[i]
		r.MachineID, r.Pattern = strings.TrimSpace(r.MachineID), strings.TrimSpace(r.Pattern)
		p := strings.ReplaceAll(r.Pattern, "\\", "/")
		absolute := strings.HasPrefix(p, "/") || p == "~" || strings.HasPrefix(p, "~/") || len(p) > 2 && p[1:3] == ":/"
		_, err := path.Match(p, "")
		if r.MachineID == "" || !absolute || err != nil || len(p) > 2048 || strings.ContainsAny(p, "\x00\r\n") {
			return fmt.Errorf("invalid directory rule: provide a machine_id and an absolute path or ~/ glob")
		}
		k := [2]string{r.MachineID, r.Pattern}
		if seen[k] {
			return errors.New("duplicate directory rule")
		}
		seen[k] = true
	}
	if v.Rules == nil {
		v.Rules = []hiddenAgentDir{}
	}
	return nil
}

func (s *Service) saveHiddenDirs(ctx context.Context, v hiddenAgentDirs, expected *hiddenAgentDirs) error {
	if err := validateHiddenDirs(&v); err != nil {
		return err
	}
	b := &s.hiddenDirs
	b.mu.Lock()
	if expected != nil {
		current := hiddenAgentDirs{}
		if st := s.store(); st != nil {
			if _, err := st.Setting(ctx, hiddenAgentDirsKey, &current); err != nil {
				b.mu.Unlock()
				return err
			}
		}
		if !slices.Equal(current.Rules, expected.Rules) {
			b.mu.Unlock()
			return errors.New("directory rules changed while awaiting confirmation; read them and try again")
		}
	}
	if err := s.putSetting(ctx, hiddenAgentDirsKey, v); err != nil {
		b.mu.Unlock()
		return err
	}
	b.value, b.loaded, b.at = v, true, time.Now()
	b.mu.Unlock()
	s.snapshotStale()
	for _, m := range s.fleetMachines() {
		s.sessionsChanged(m.ID)
	}
	return nil
}

// Active supervised tasks stay visible: hiding ambient activity must never sever
// a task's supervision. Merely having been started by Persona is not an exemption.
func (s *Service) hiddenByDirectory(machine string, sess proto.Session) bool {
	if sess.Assistant {
		return false
	}
	v, err := s.readHiddenDirs(context.Background())
	matched := err != nil // Do not expose ambient context after a failed settings read.
	if len(v.Rules) == 0 && err == nil {
		return false
	}
	m, _ := s.fleetMachine(machine)
	if m.isAssistant(sess) {
		return false
	}
	for _, r := range v.Rules {
		if r.Enabled && r.MachineID == machine && directoryMatches(sess.Cwd, m.Home, r.Pattern) {
			matched = true
			break
		}
	}
	return matched && !s.supervisedTurn(machine, sess.SessionID, time.Time{})
}

func (s *Service) directoryHiddenSession(machine, sid string) bool {
	sess, ok := s.nodeSession(machine, sid)
	return ok && s.hiddenByDirectory(machine, sess)
}

func (s *Service) hiddenDirSetting() ownSetting {
	return ownSetting{Setting: Setting{Key: hiddenAgentDirsKey, Title: i18n.T(i18n.Default, "assistant-hidden_dirs.title"), Actions: []string{"get", "set"},
		About: "Hide ambient Agent sessions by working directory on each computer; all runtimes. Active supervised tasks remain visible. Disabling a rule restores visibility; collection continues.",
		Value: "{rules:[{machine_id,pattern,enabled}]}; complete replacement; absolute paths or ~/ globs (*, ?, [], **); directory descendants included"},
		get: func(ctx context.Context) (string, error) {
			v, err := s.readHiddenDirs(ctx)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(v)
			return string(b), err
		},
		set: func(ctx context.Context, raw json.RawMessage) (*ownChange, error) {
			var v hiddenAgentDirs
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			if err := validateHiddenDirs(&v); err != nil {
				return nil, err
			}
			before, err := s.readHiddenDirs(ctx)
			if err != nil {
				return nil, err
			}
			if slices.Equal(before.Rules, v.Rules) {
				return &ownChange{summary: "Directory visibility rules are unchanged."}, nil
			}
			old, _ := json.Marshal(before)
			next, _ := json.Marshal(v)
			return &ownChange{summary: "Directory visibility rules updated", cardText: func(l i18n.Lang) string {
				return i18n.T(l, "assistant-hidden_dirs.confirm", i18n.Args{"before": string(old), "after": string(next)})
			},
				apply: func(ctx context.Context) (string, error) {
					err := s.saveHiddenDirs(ctx, v, &before)
					return "Directory visibility rules updated. Restore the previous rules to undo.", err
				}}, nil
		}}
}

func (s *Service) hiddenDirRoutes() {
	s.handle("GET /api/v1/assistant/hidden-agent-dirs", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.readHiddenDirs(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, v)
	})
	s.handle("PUT /api/v1/assistant/hidden-agent-dirs", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change directory rules"))
			return
		}
		var v hiddenAgentDirs
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&v); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := validateHiddenDirs(&v); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := s.saveHiddenDirs(r.Context(), v, nil); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, v)
	})
}
