package kb

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// The marks store only the position and identity of the latest observed change per source. The content and state
// stay at their canonical source. Historical metadata without these receipts has no known evidence boundary.
type projectEvidence struct {
	Position int                       `json:"position"`
	Marks    map[string]projectMark    `json:"marks,omitempty"`
	Fields   map[string]projectReceipt `json:"fields,omitempty"`
}

type projectMark struct {
	Position   int    `json:"position"`
	Version    string `json:"version"`
	Pending    bool   `json:"pending,omitempty"`
	LogVersion string `json:"log_version,omitempty"`
	LogHash    string `json:"log_hash,omitempty"`
}

type projectReceipt struct {
	Cutoff int      `json:"cutoff"`
	Writer string   `json:"writer"`
	Refs   []string `json:"source_refs,omitempty"`
}

func (p *Project) evidence() *projectEvidence {
	if p.FieldEvidence == nil {
		p.FieldEvidence = &projectEvidence{}
	}
	return p.FieldEvidence
}

func (p *Project) noteFact(ref, version string) bool {
	e := p.evidence()
	if e.Marks == nil {
		e.Marks = map[string]projectMark{}
	}
	if old, ok := e.Marks[ref]; ok && old.Version == version {
		return false
	}
	e.Position++
	mark := e.Marks[ref]
	mark.Position, mark.Version = e.Position, version
	e.Marks[ref] = mark
	return true
}

func (p *Project) localRef(field string) string { return KindProject + "/" + p.Slug + "/" + field }

// cover records only the inputs actually used by this field's writer. A source outside the rewrite's bounded
// inputs, or one the manual edit did not cite, prevents the revision from crossing its outstanding position.
func (p *Project) cover(field, writer string, refs []string) {
	e := p.evidence()
	if e.Fields == nil {
		e.Fields = map[string]projectReceipt{}
	}
	cutoff := e.Position
	for ref, mark := range e.Marks {
		if mark.Position > e.Fields[field].Cutoff && (mark.Pending || !slices.Contains(refs, ref)) {
			cutoff = min(cutoff, mark.Position-1)
		}
	}
	refs = slices.Clone(refs)
	slices.Sort(refs)
	e.Fields[field] = projectReceipt{Cutoff: cutoff, Writer: writer, Refs: slices.Compact(refs)}
}

func (p *Project) modelSourceRefs(in prompts.KBProjectInput) []string {
	var refs []string
	if in.Summary != "" {
		refs = append(refs, p.localRef("summary"))
	}
	if len(in.Log) > 0 {
		if p.FieldEvidence == nil || p.FieldEvidence.Marks[p.localRef("log")].LogHash == "" {
			refs = append(refs, p.localRef("log"))
		}
		if p.FieldEvidence != nil {
			seen := make(map[string]bool, len(in.Log))
			for _, line := range in.Log {
				seen[projectVersion(line)] = true
			}
			for ref, mark := range p.FieldEvidence.Marks {
				if seen[mark.LogHash] {
					refs = append(refs, ref)
				}
			}
		}
	}
	for _, c := range in.Chats {
		if c.Summary != "" || c.State != "" {
			refs = append(refs, KindChat+"/"+c.ID)
		}
	}
	for _, m := range in.Moments {
		refs = append(refs, "moments/"+m.ID)
	}
	return refs
}

// Project list reads use only the metadata already loaded. The detail alone resolves canonical sources.
func (p *Project) knowledge() *kbwire.ProjectKnowledge {
	k := &kbwire.ProjectKnowledge{Policy: p.policy(), Fields: map[string]kbwire.ProjectField{}}
	e := p.FieldEvidence
	if e != nil {
		k.Position = e.Position
	}
	for _, field := range []string{"summary", "one_line"} {
		f := kbwire.ProjectField{Status: "unknown", Locked: p.fieldLocked(field), Maintainer: "pipeline"}
		if holder := heldBy(&p.Tagging, field); holder != "" {
			f.Maintainer = holder
		}
		if k.Policy == referenceOnly {
			f.Maintainer = "external"
		}
		if e != nil {
			if r, ok := e.Fields[field]; ok {
				f.Status, f.Cutoff, f.Writer, f.SourceRefs = "current", &r.Cutoff, r.Writer, r.Refs
				f.Uncovered = k.Position - r.Cutoff
			} else {
				f.Uncovered = k.Position // only the changes actually seen since tracking began
			}
		}
		if f.Uncovered > 0 {
			f.Status = "needs_review"
			k.NeedsReview = true
		}
		k.Fields[field] = f
	}
	return k
}

func (e *Engine) projectSources(p *Project) []kbwire.ProjectSource {
	refs := make(map[string]kbwire.ProjectSource)
	add := func(ref string) { refs[ref] = kbwire.ProjectSource{Ref: ref} }
	for _, ref := range p.SourceRefs {
		add(ref)
	}
	for _, ref := range p.Items {
		add(ref)
	}
	for _, m := range p.Moments {
		add("moments/" + m.ID)
	}
	if p.FieldEvidence != nil {
		for ref := range p.FieldEvidence.Marks {
			add(ref)
		}
		for _, receipt := range p.FieldEvidence.Fields {
			for _, ref := range receipt.Refs {
				add(ref)
			}
		}
	}
	for _, id := range p.Chats {
		add(KindChat + "/" + id)
	}
	keys := slices.Sorted(maps.Keys(refs))
	out := make([]kbwire.ProjectSource, 0, len(keys))
	for _, ref := range keys {
		source := refs[ref]
		if id, ok := strings.CutPrefix(ref, KindChat+"/"); ok && !strings.Contains(id, "/") {
			if c, ok := e.Store.Chat(id); ok && c.State != "" {
				source.State = c.State
				if !c.StateAt.IsZero() {
					source.StateAt = &c.StateAt
				}
			}
		}
		out = append(out, source)
	}
	return out
}

func (p *Project) entry() Entry {
	return Entry{Kind: KindProject, ID: p.Slug, Name: p.Name, OneLine: p.OneLine, Importance: p.Importance,
		Relevance: p.Relevance, Tags: p.Tags, Updated: p.Updated, Extra: p.Kind + " · " + p.Status, ProjectKnowledge: p.knowledge()}
}

// The disk receipts and explicitly chosen rules always win over an older metadata writer. A reference-only
// project permits classification and links; an in-flight derivation cannot refill any of its retained content.
func protectProjectWrite(p *Project, current, incoming map[string]json.RawMessage, manual bool) {
	copyField := func(key string) {
		if val, ok := current[key]; ok {
			incoming[key] = val
		} else {
			delete(incoming, key)
		}
	}
	copyField("field_evidence")
	if !p.setStoragePolicy {
		copyField("storage_policy")
	}
	if !p.setSourceRefs {
		copyField("source_refs")
	}
	if len(current["field_evidence"]) != 0 {
		copyField("one_line")
		var newest projectEvidence
		_ = json.Unmarshal(current["field_evidence"], &newest)
		if (p.FieldEvidence == nil || p.FieldEvidence.Position < newest.Position) && string(current["dirty"]) == "true" {
			copyField("dirty")
		}
	}
	if len(p.forgottenSources) > 0 {
		eraseProjectWrite(current, incoming, p.forgottenSources)
	}
	var policy string
	_ = json.Unmarshal(incoming["storage_policy"], &policy)
	if policy != referenceOnly {
		return
	}
	delete(incoming, "dirty")
	for _, name := range []string{"one_line", "goal", "my_role", "note", "pinned", "keywords"} {
		copyField(name)
	}
	if len(p.forgottenSources) == 0 {
		copyField("moments")
	}
	if !manual && len(current) > 0 {
		copyField("status")
	}
}
