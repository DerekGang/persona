// Package badadapt converts fleetcount's values the way an adapter must not.
//
//persona:layer adapter
package badadapt

import (
	"github.com/nwverse/persona/server/internal/assistant" // adapter-no-application
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/inbox/item/itemadapt" // adapter-import
)

// Agent is a record named like the core's own type.
type Agent struct { // adapter-name
	Service *assistant.Service
	Record  itemadapt.Record
}

// WhyWord hands the reason out as a word.
func WhyWord(w fleetcount.Why) string { // adapter-exported-result
	if w.Counted() {
		return "needs_you"
	}
	return "held"
}
