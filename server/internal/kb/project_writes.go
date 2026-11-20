package kb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

func chatEvidenceVersion(c *Chat) string {
	return projectVersion(fmt.Sprintf("%d/%s/%s", c.Messages, c.State, c.StateAt.UTC().Format(time.RFC3339Nano)))
}

func projectVersion(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// An explicit pointer to an already archived chat subscribes its project to that source without changing the chat's
// filing or starting a connector. Populate the inverse lookup once per store or explicit index edit.
func (s *Store) projectsObservingChat(c *Chat) []string {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	if s.chatRefProjects == nil {
		s.chatRefProjects = make(map[string][]string)
		for _, p := range s.Projects() {
			for _, ref := range p.SourceRefs {
				if id, ok := strings.CutPrefix(ref, KindChat+"/"); ok {
					s.chatRefProjects[id] = append(s.chatRefProjects[id], p.Slug)
				}
			}
		}
	}
	return addUnique(slices.Clone(c.Projects), s.chatRefProjects[c.ID]...)
}

// observeChatState rereads the canonical source under the metadata lock, so a late notification cannot restore an
// older source revision. Replayed batches retain the same position. A reference-only project records the pointer.
func (s *Store) observeChatState(slug, id string, arrived, compressed bool) (err error) {
	s.metaMu.Lock()
	changed := false
	defer func() {
		s.metaMu.Unlock()
		if changed && err == nil {
			s.notify(KindProject, slug, "metadata")
		}
	}()
	p, ok := s.Project(slug)
	ref := KindChat + "/" + id
	if !ok || !p.Live() || p.Status == "ignored" || !slices.Contains(p.Chats, id) && !slices.Contains(p.SourceRefs, ref) {
		return nil
	}
	c, ok := s.Chat(id)
	if !ok {
		return nil
	}
	if compressed && (p.FieldEvidence == nil || !p.FieldEvidence.Marks[ref].Pending || c.Pending != 0 || c.fieldLocked("summary")) {
		return nil
	}
	// Content and connector state are one canonical source, regardless of which projects list it.
	version := chatEvidenceVersion(c)
	changed = p.noteFact(ref, version)
	mark := p.FieldEvidence.Marks[ref]
	if arrived && c.Pending > 0 && !mark.Pending {
		mark.Pending = true
		changed = true
	}
	if compressed && mark.Pending {
		mark.Pending = false
		changed = true
	}
	p.FieldEvidence.Marks[ref] = mark
	if !changed {
		return nil
	}
	if p.policy() != referenceOnly {
		p.Dirty = true
	}
	return writeJSON(s.entityPath(KindProject, slug), p)
}

// projectLog guards the policy and appends while both metadata and the timeline are frozen. A known source revision
// can be replayed; the display label and the business text never become its identity.
func (s *Store) projectLog(slug string, at time.Time, label, line, ref, version string) (err error) {
	if s.readOnly {
		return ErrReadOnly
	}
	s.metaMu.Lock()
	s.mu.Lock()
	wrote := false
	defer func() {
		s.mu.Unlock()
		s.metaMu.Unlock()
		if wrote {
			s.notify(KindProject, slug, "log")
		}
	}()
	p, ok := s.Project(slug)
	if ok && p.policy() == referenceOnly {
		return fmt.Errorf("%w: log", ErrReferenceOnly)
	}
	if !ok && ref != "" {
		return nil
	} // a removed project must not be recreated by a late derivation
	stale := false
	if id, chat := strings.CutPrefix(ref, KindChat+"/"); chat {
		if current, seen := s.Chat(id); seen {
			stale = chatEvidenceVersion(current) != version
		}
	}
	if ok && ref != "" && p.FieldEvidence != nil {
		if mark, seen := p.FieldEvidence.Marks[ref]; seen && mark.LogVersion == version {
			return nil
		}
	}
	if _, err := s.settleLogLocked(slug); err != nil {
		return err
	}
	zone := s.zoneLocked()
	path := s.path(KindProject, slug, "log.md")
	info, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	undo := func() error {
		if info != nil {
			return os.Truncate(path, info.Size())
		}
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	record := fmt.Sprintf("- %s · %s · %s", at.In(zone).Format("2006-01-02 15:04"), label, oneLine(line))
	if err = appendFile(path, logHeader(zone), record+"\n"); err != nil {
		return errors.Join(err, undo())
	}
	wrote = true
	if !ok {
		return nil
	} // direct timeline imports can predate the project's first metadata
	if ref == "" {
		ref = p.localRef("log")
		// A public log write has no stable external event ID. Each successful append is new evidence.
		version = fmt.Sprint(p.evidence().Position + 1)
	}
	// A late digest may add its historic timeline line, but cannot stand for the newer source fact.
	if !stale {
		p.noteFact(ref, version)
		mark := p.FieldEvidence.Marks[ref]
		mark.LogVersion, mark.LogHash = version, projectVersion(record)
		p.FieldEvidence.Marks[ref] = mark
	}
	p.Dirty = true
	if err := writeJSON(s.entityPath(KindProject, slug), p); err != nil {
		rollback := undo()
		wrote = rollback != nil
		return errors.Join(err, rollback)
	}
	return nil
}

// writeProjectSummary records the writer only when that actual write is known. The existing lock holder may differ
// from a historical writer. A summary edit is new evidence for the derived one-line description.
func (s *Store) writeProjectSummary(slug, text, writer string, cited []string) (err error) {
	if s.readOnly {
		return ErrReadOnly
	}
	s.metaMu.Lock()
	changed := false
	defer func() {
		s.metaMu.Unlock()
		if err == nil && changed {
			s.notify(KindProject, slug, "summary")
			s.notify(KindProject, slug, "metadata")
		}
	}()
	p, ok := s.Project(slug)
	if !ok {
		return ErrNotFound
	}
	if p.policy() == referenceOnly {
		return fmt.Errorf("%w: summary", ErrReferenceOnly)
	}
	prior, err := json.Marshal(p)
	if err != nil {
		return err
	}
	next := []byte(strings.TrimSpace(text) + "\n")
	ref := p.localRef("summary")
	p.noteFact(ref, projectVersion(string(next)))
	p.cover("summary", writer, append(slices.Clone(cited), ref))
	p.Dirty = true
	changed, err = s.saveProjectText(prior, p, next)
	return err
}

// saveProjectText writes a changed conclusion with its receipt under metaMu. An interrupted process may leave
// unknown coverage, but never a previous writer attached to replacement text. A failed text write restores the
// previous receipt when possible.
func (s *Store) saveProjectText(prior []byte, p *Project, next []byte) (bool, error) {
	meta := s.entityPath(KindProject, p.Slug)
	if next == nil {
		return false, writeJSON(meta, p)
	}
	path := s.path(KindProject, p.Slug, "summary.md")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if bytes.Equal(old, next) {
		return false, writeJSON(meta, p)
	}
	var previous Project
	if err := json.Unmarshal(prior, &previous); err != nil {
		return false, err
	}
	invalidated := false
	if previous.FieldEvidence != nil {
		if _, known := previous.FieldEvidence.Fields["summary"]; known {
			delete(previous.FieldEvidence.Fields, "summary")
			if err := writeJSON(meta, &previous); err != nil {
				return false, err
			}
			invalidated = true
		}
	}
	if err := writeFile(path, next); err != nil {
		if invalidated {
			err = errors.Join(err, writeFile(meta, prior))
		}
		return false, err
	}
	if err := writeJSON(meta, p); err != nil {
		// The old receipt is already unknown. Restore it only after restoring the old bytes.
		var undo error
		if old == nil {
			undo = os.Remove(path)
		} else {
			undo = writeFile(path, old)
		}
		if undo == nil && invalidated {
			undo = writeFile(meta, prior)
		}
		return undo != nil, errors.Join(err, undo)
	}
	return true, nil
}
