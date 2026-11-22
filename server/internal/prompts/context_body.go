package prompts

import (
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// closingTag is anything a reader of a message could take for the end of a <persona-context>
// block: either name (#3182), any case, spaces around the slash and before `>` (the apps match it as loosely, web
// lib/context-body.ts).
var closingTag = lazyre.New(`(?i)<\s*/\s*persona-context\s*>`)

// ContextBody is a text to go inside a context block with nothing left in it that closes the block, so what follows
// cannot sit outside it as the user's own words (a quoted or forwarded message, a mail handed to the AI twin, comes
// from someone else; #3754). Closing tags are removed, and one the removal itself puts together
// ("</persona-</persona-context>context>") has its `<` written as `&lt;`, which joins nothing: two passes, linear in
// the text. Templates call it as `ctx`.
func ContextBody(s string) string {
	s = closingTag.ReplaceAllString(s, "")
	return closingTag.ReplaceAllStringFunc(s, func(tag string) string { return "&lt;" + tag[1:] })
}

// shownTag is the start of any tag the apps and the hub read out of a message's text instead of showing it: a context
// block's opening or closing and a file's (either name), the photos and pasted text a message carries (web
// lib/turns.ts parseUserText), any case, spaces after `<` and around the slash.
var shownTag = lazyre.New(`(?i)<(\s*/?\s*(?:` + compat.ContextPattern() + `|` + compat.FilePattern() + `|attached_images|pasted_content))`)

// PlainWords is someone else's text that stands as a message's own words after Persona's framing block (a partner
// hub's message): no tag in it opens, closes or forges a block, a file or a photo, each one's `<` written as `&lt;`, so
// the apps show it and the model reads it as the words it is. Templates call it as `plain`.
func PlainWords(s string) string {
	return shownTag.ReplaceAllString(s, "&lt;$1")
}
