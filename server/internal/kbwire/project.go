package kbwire

import "time"

// ProjectKnowledge separates a project's modification time from the evidence its conclusions cover.
// Position is a project-local revision. Old hubs omit this object; a project without receipts has unknown fields.
type ProjectKnowledge struct {
	Policy      string                  `json:"policy"`
	Position    int                     `json:"position"`
	NeedsReview bool                    `json:"needs_review"`
	Fields      map[string]ProjectField `json:"fields"`
	Sources     []ProjectSource         `json:"sources,omitempty"` // included in detail only
}

type ProjectField struct {
	Status     string   `json:"status"` // current, needs_review or unknown
	Cutoff     *int     `json:"cutoff,omitempty"`
	Uncovered  int      `json:"uncovered,omitempty"`
	Writer     string   `json:"writer,omitempty"`
	Maintainer string   `json:"maintainer,omitempty"`
	Locked     bool     `json:"locked,omitempty"`
	SourceRefs []string `json:"source_refs,omitempty"`
}

// ProjectSource identifies the original source. For a linked chat, State is what its connector reported.
type ProjectSource struct {
	Ref     string     `json:"ref"`
	State   string     `json:"state,omitempty"`
	StateAt *time.Time `json:"state_at,omitempty"`
}
