package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type todoRepositoryImportStore interface {
	ImportTodoRepositoryLinks(context.Context, string, []string, string) (repository.TaskSet, bool, error)
}

type todoRepositoryLinkDetailsStore interface {
	ImportTodoRepositoryLinkDetails(context.Context, string, []repository.LinkInput, string) (repository.TaskSet, bool, error)
}

func knownTodoLinks(todo Todo) []string {
	out := []string{}
	for _, raw := range append(append([]string{}, todo.PRs...), todo.PRURL) {
		if _, link, err := repository.ParseTaskLink(raw); err == nil {
			out = append(out, link.CanonicalURL)
		}
	}
	return out
}

func (s *Service) importTodoRepositoryLinks(ctx context.Context, id string, urls []string, source string) error {
	st, ok := s.store().(todoRepositoryImportStore)
	if !ok {
		return nil
	} // Older/in-memory adapters retain their existing task behavior.
	_, changed, err := st.ImportTodoRepositoryLinks(ctx, id, urls, source)
	if err == nil && changed {
		s.todosChanged(ctx)
	}
	return err
}

func (s *Service) importLegacyTodoRepositoryLinks(ctx context.Context, id string) error {
	if _, ok := s.store().(todoRepositoryImportStore); !ok {
		return nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	todo, ok := s.todoNow(id)
	if !ok {
		return db.ErrTodoNotFound
	}
	return s.importTodoRepositoryLinks(ctx, id, knownTodoLinks(todo), "legacy")
}

func (s *Service) toolTodoRepositoryLinksAdd(ctx context.Context, machine, self, name string, args json.RawMessage) (string, error) {
	var in struct {
		ID    string   `json:"id"`
		URLs  []string `json:"urls"`
		Links []struct {
			URL   string `json:"url"`
			Title string `json:"title,omitempty"`
		} `json:"links"`
	}
	if err := decodeTaskRepositoryInput(args, &in); err != nil {
		return "", err
	}
	if count := len(in.URLs) + len(in.Links); count == 0 || count > 50 {
		return "", repository.Problem("invalid")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	todo, ok := s.todoNow(in.ID)
	if !ok {
		return "", db.ErrTodoNotFound
	}
	if ok, err := s.confirmOutbound(ctx, machine, self, name, todo.Title, args); err != nil || !ok {
		return todoNotAllowed(err)
	}
	if _, ok := s.store().(todoRepositoryImportStore); !ok {
		return "", repository.Problem("store_unavailable")
	}
	if err := s.importLegacyTodoRepositoryLinks(ctx, in.ID); err != nil {
		return "", err
	}
	if len(in.Links) == 0 {
		if err := s.importTodoRepositoryLinks(ctx, in.ID, in.URLs, "explicit"); err != nil {
			return "", err
		}
	} else {
		st, ok := s.store().(todoRepositoryLinkDetailsStore)
		if !ok {
			return "", repository.Problem("store_unavailable")
		}
		links := make([]repository.LinkInput, 0, len(in.URLs)+len(in.Links))
		for _, raw := range in.URLs {
			links = append(links, repository.LinkInput{URL: raw})
		}
		for _, link := range in.Links {
			links = append(links, repository.LinkInput{URL: link.URL, Title: link.Title})
		}
		_, changed, err := st.ImportTodoRepositoryLinkDetails(ctx, in.ID, links, "explicit")
		if err != nil {
			return "", err
		}
		if changed {
			s.todosChanged(ctx)
		}
	}
	set, err := s.readTodoRepositories(ctx, in.ID)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(set)
	return string(raw), err
}

// Keep an exact canonical association list in agent context instead of a second PR list.
func formatTodoRepositoryContext(set repository.TaskSet) string {
	text := fmt.Sprintf("repositories (revision %d):\n", set.Revision)
	for _, row := range set.Repositories {
		text += fmt.Sprintf("- %s %s\n", row.RepoID, row.SourceSnapshot.Identity.CanonicalURL)
		for _, link := range row.Links {
			text += fmt.Sprintf("  %s %s\n", link.Kind, link.CanonicalURL)
			if link.Title != "" {
				text += fmt.Sprintf("    title: %s\n", link.Title)
			}
		}
	}
	return text
}

// Task agents use the same store and tools, bounded by their active session link.
func (s *Service) agentRepositoryTool(ctx context.Context, machine, session, name string, args json.RawMessage) (string, error) {
	if session == "" {
		return "", errors.New("Persona could not tell which agent session is calling")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	todo, ok := s.openTodoOfAgent(machine, session)
	if !ok {
		return "", errors.New("you are not working on an open todo")
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.ID != todo.ID {
		return "", errors.New("repository tools can access only the todo this agent works on; read todo_decisions for its ID")
	}
	if name == "todo_repository_links_add" {
		return s.toolTodoRepositoryLinksAdd(ctx, machine, session, name, args)
	}
	return s.toolTodoRepositories(ctx, machine, session, name, args)
}

func taskRepositoryReportLinks(text string) []string {
	if block := deliveryCloseout(text); block != "" {
		_, artifacts, _ := closeout.Parse(block)
		return taskRepositoryArtifactLinks(artifacts)
	}
	urls := []string{}
	for _, raw := range inboxTaskURL.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, ".,;:!?)]}，。；：！）》」』") // i18n-ignore: report punctuation, not displayed text
		if _, link, err := repository.ParseTaskLink(raw); err == nil && link.Kind == "pull_request" {
			urls = append(urls, link.CanonicalURL)
		}
	}
	return urls
}

func taskRepositoryArtifactLinks(artifacts []proto.WorkArtifact) []string {
	urls := []string{}
	for _, artifact := range artifacts {
		if artifact.Kind != "pr" {
			continue
		}
		u, err := url.Parse(artifact.Ref)
		if err != nil {
			continue
		}
		u.User = nil
		u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
		u.Path = strings.TrimRight(u.Path, "/")
		identity, link, err := repository.ParseTaskLink(u.String())
		if err != nil || link.Kind != "pull_request" {
			continue
		}
		// Validate before discarding a query or fragment, including credentials.
		if _, err := repository.Parse(artifact.Ref, identity.Provider); err == nil {
			urls = append(urls, link.CanonicalURL)
		}
	}
	return urls
}

// Artifacts also arrive through stopped-turn triage, without a full transcript
// subscription. Only the session's current task may inherit that turn's PRs.
func (s *Service) importTaskRepositoryArtifacts(ctx context.Context, e db.SessionWork) error {
	urls := taskRepositoryArtifactLinks(e.Work.Artifacts)
	if len(urls) == 0 || e.TurnAt.IsZero() || e.Work.RetiredAt != nil {
		return nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	todo, ok := s.openTodoOfAgent(e.MachineID, e.SessionID)
	if !ok {
		return nil
	}
	for _, agent := range todo.Agents {
		if agent.MachineID == e.MachineID && agent.SessionID == e.SessionID &&
			agent.ReleasedAt == nil && agent.DismissedAt == nil && !e.TurnAt.Before(agent.LinkedAt) {
			return s.importTodoRepositoryLinks(ctx, todo.ID, urls, "report")
		}
	}
	return nil
}
