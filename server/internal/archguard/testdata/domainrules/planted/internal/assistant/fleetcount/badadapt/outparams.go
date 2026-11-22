package badadapt

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// View is a struct of the adapter's own, filled for application code to read.
type View struct {
	Why     string // adapter-exported-result
	Counted bool   // adapter-exported-result
}

// Fill hands the reason out through an out-parameter of the adapter's own struct.
func Fill(w fleetcount.Why, v *View) { // adapter-exported-result
	v.Why, v.Counted = WhyWord(w), w.Counted()
}

// WhyInto hands the reason out through a pointer to a word.
func WhyInto(w fleetcount.Why, dst *string) { // adapter-exported-result
	*dst = WhyWord(w)
}

// EachWhy hands the reason out to a callback.
func EachWhy(w fleetcount.Why, f func(word string)) { // adapter-exported-result
	f(WhyWord(w))
}

// Sink is what application code implements to be told the reason as a word.
type Sink interface{ Why(word string) }

// Tell hands the reason out through an interface the adapter declares.
func Tell(w fleetcount.Why, s Sink) { // adapter-exported-result
	s.Why(WhyWord(w))
}

// Words hands the reason out into a map, by the row's place.
func Words(w fleetcount.Why, at slot.Slot, into map[slot.Slot]string) { // adapter-exported-result
	into[at] = WhyWord(w)
}
