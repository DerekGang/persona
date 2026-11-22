package badcore

import "github.com/nwverse/persona/server/internal/domainkit/outcome"

func ignore(raw string) {
	_ = ParseWords(raw) // core-outcome-return: a discarded outcome
}

func unset(raw string) (out outcome.Of[Words, Fail]) {
	return // core-outcome-return: an unset outcome
}
