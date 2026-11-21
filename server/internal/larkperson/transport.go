package larkperson

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// Transport is the narrow configured lark-cli surface used by Service. Tests use fakes; production uses RunnerTransport.
type Transport interface {
	AuthStatus(context.Context) (adapter.AuthStatus, error)
	SearchPeople(context.Context, string, int) ([]Person, error)
	ListMessages(context.Context, string, string, string, int) (adapter.MessagePage, error)
	SendText(context.Context, string, string, string) (json.RawMessage, error)
	ReplyText(context.Context, string, string, string, bool) (json.RawMessage, error)
}

// RunnerTransport delegates bot writes to the existing typed Runner methods. Credentials stay in lark-cli configuration.
type RunnerTransport struct {
	Runner *adapter.Runner
}

func NewRunnerTransport(runner *adapter.Runner) *RunnerTransport {
	return &RunnerTransport{Runner: runner}
}

func (t *RunnerTransport) AuthStatus(ctx context.Context) (adapter.AuthStatus, error) {
	if t == nil || t.Runner == nil {
		return adapter.AuthStatus{}, errors.New("lark runner unavailable")
	}
	return t.Runner.AuthStatus(ctx)
}

func (t *RunnerTransport) SearchPeople(ctx context.Context, query string, limit int) ([]Person, error) {
	if t == nil || t.Runner == nil {
		return nil, errors.New("lark runner unavailable")
	}
	raw, err := t.Runner.SearchLimit(ctx, "people", query, limit)
	if err != nil {
		return nil, err
	}
	return decodePeople(raw)
}

func (t *RunnerTransport) ListMessages(ctx context.Context, chatID, threadID, before string, limit int) (adapter.MessagePage, error) {
	if t == nil || t.Runner == nil {
		return adapter.MessagePage{}, errors.New("lark runner unavailable")
	}
	return t.Runner.ListMessages(ctx, chatID, threadID, before, limit)
}

func (t *RunnerTransport) SendText(ctx context.Context, chatID, text, key string) (json.RawMessage, error) {
	if t == nil || t.Runner == nil {
		return nil, errors.New("lark runner unavailable")
	}
	return t.Runner.SendText(ctx, chatID, text, key)
}

func (t *RunnerTransport) ReplyText(ctx context.Context, messageID, text, key string, inThread bool) (json.RawMessage, error) {
	if t == nil || t.Runner == nil {
		return nil, errors.New("lark runner unavailable")
	}
	return t.Runner.ReplyText(ctx, messageID, text, key, inThread)
}

func decodePeople(raw json.RawMessage) ([]Person, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("invalid lark people directory response")
	}
	people := []Person{}
	seen := map[string]bool{}
	var walk func(any, int) error
	walk = func(v any, depth int) error {
		if depth > 8 {
			return errors.New("invalid lark people directory nesting")
		}
		switch x := v.(type) {
		case []any:
			for _, child := range x {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case map[string]any:
			id := stringValue(x, "open_id")
			if id == "" {
				id = stringValue(x, "user_id")
			}
			if id != "" {
				if !validOpaqueID(id) || seen[id] {
					return errors.New("invalid or duplicate lark directory identity")
				}
				seen[id] = true
				people = append(people, Person{OpenID: id, Name: strings.TrimSpace(stringValue(x, "name"))})
				return nil
			}
			for _, key := range []string{"users", "items", "people", "results", "data"} {
				if child, ok := x[key]; ok {
					if err := walk(child, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(value, 0); err != nil {
		return nil, err
	}
	return people, nil
}

func stringValue(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}

func responseMessageID(raw json.RawMessage) string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var walk func(any, int) string
	walk = func(v any, depth int) string {
		if depth > 8 {
			return ""
		}
		switch x := v.(type) {
		case map[string]any:
			if id := stringValue(x, "message_id"); validOpaqueID(id) {
				return id
			}
			for _, key := range []string{"data", "message", "result"} {
				if id := walk(x[key], depth+1); id != "" {
					return id
				}
			}
		case []any:
			for _, child := range x {
				if id := walk(child, depth+1); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(value, 0)
}
