package kb

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

const referenceOnly = "reference_only"

var ErrReferenceOnly = errors.New("reference-only project cannot store detail")

func (p *Project) policy() string {
	if p.StoragePolicy == referenceOnly {
		return referenceOnly
	}
	return "managed"
}

func canonicalProjectRefs(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if len(ref) > 2048 || ref == "" || strings.IndexFunc(ref, unicode.IsSpace) >= 0 || strings.IndexFunc(ref, unicode.IsControl) >= 0 {
			return nil, errors.New("invalid project source reference")
		}
		u, err := url.Parse(ref)
		switch {
		case err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil:
			u.Host = strings.ToLower(u.Host)
			ref = u.String()
		default:
			kind, id, ok := strings.Cut(ref, "/")
			if !ok || id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#") ||
				!slices.Contains([]string{KindChat, KindDoc, KindEvent, KindMeeting, KindPerson, KindProject, KindRecording, KindTopic, "moments"}, kind) {
				return nil, errors.New("source_refs requires an HTTP(S) URL without credentials or a knowledge kind/id")
			}
		}
		if !slices.Contains(out, ref) {
			out = append(out, ref)
		}
		if len(out) > 64 {
			return nil, errors.New("a project accepts at most 64 explicit source references")
		}
	}
	return out, nil
}

func (p *Project) applyPolicy(u Update) error {
	if u.StoragePolicy != "" && u.StoragePolicy != "managed" && u.StoragePolicy != referenceOnly {
		return errors.New("storage_policy must be managed or reference_only")
	}
	if u.SourceRefs != nil {
		refs, err := canonicalProjectRefs(u.SourceRefs)
		if err != nil {
			return err
		}
		p.SourceRefs = refs
		p.setSourceRefs = true
	}
	if u.StoragePolicy != "" {
		if p.policy() == referenceOnly && u.StoragePolicy == "managed" {
			p.Dirty = true
		}
		p.StoragePolicy = u.StoragePolicy
		p.setStoragePolicy = true
	}
	if p.policy() == referenceOnly {
		for _, field := range setFields(u) {
			if slices.Contains([]string{"summary", "log", "goal", "my_role", "note", "pinned", "merge_into"}, field) {
				return fmt.Errorf("%w: %s", ErrReferenceOnly, field)
			}
		}
	}
	return nil
}
