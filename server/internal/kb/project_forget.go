package kb

import (
	"encoding/json"
	"maps"
	"slices"
)

// Explicit connector erasure also removes pointers from field receipts and from a project's source index. A normal
// edit of an alias or external pointer does not erase evidence history.
func forgottenProjectRefs(chats, people, posts []string, items map[string]bool) map[string]bool {
	refs := maps.Clone(items)
	if refs == nil {
		refs = map[string]bool{}
	}
	for _, id := range chats {
		refs[KindChat+"/"+id] = true
	}
	for _, id := range people {
		refs[KindPerson+"/"+id] = true
	}
	for _, id := range posts {
		refs["moments/"+id] = true
	}
	return refs
}

func (p *Project) eraseSourceEvidence(gone map[string]bool) bool {
	remove := func(list []string) ([]string, bool) {
		kept := slices.DeleteFunc(list, func(ref string) bool { return gone[ref] })
		return kept, len(kept) != len(list)
	}
	var changed bool
	p.SourceRefs, changed = remove(p.SourceRefs)
	if m := p.Merge; m != nil {
		var cut bool
		m.SourceRefs, cut = remove(m.SourceRefs)
		changed = changed || cut
	}
	e := p.FieldEvidence
	if e == nil {
		return changed
	}
	evidenceChanged := false
	for ref := range e.Marks {
		if gone[ref] {
			delete(e.Marks, ref)
			evidenceChanged = true
		}
	}
	for name, r := range e.Fields {
		refs, cut := remove(r.Refs)
		if cut {
			r.Refs = refs
			e.Fields[name] = r
			evidenceChanged = true
		}
	}
	if evidenceChanged {
		e.Position++
	}
	return changed || evidenceChanged
}

// Replay the erasure on the latest version so a concurrent write to another source keeps its receipt.
func eraseProjectWrite(current, incoming map[string]json.RawMessage, gone map[string]bool) {
	prior, _ := json.Marshal(current)
	var p Project
	if json.Unmarshal(prior, &p) != nil || !p.eraseSourceEvidence(gone) {
		return
	}
	if len(p.SourceRefs) == 0 {
		delete(incoming, "source_refs")
	} else {
		incoming["source_refs"], _ = json.Marshal(p.SourceRefs)
	}
	if p.FieldEvidence != nil {
		incoming["field_evidence"], _ = json.Marshal(p.FieldEvidence)
	}
}
