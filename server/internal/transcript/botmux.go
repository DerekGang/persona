package transcript

import (
	"encoding/json"
	"html"
	"path"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// BotMux (the Lark topic ↔ coding CLI bridge, docs/integrations/engines.md "botmux") hands the agent each Lark
// message wrapped in blocks for the model: <user_message> around what was written, <sender />, <mentions>,
// <attachments>, <message_location />, <botmux_reminder>, <botmux_formatting> and the like, and the agent answers
// with `botmux send`. BotmuxMessage and BotmuxSend read those back for the apps (proto.Botmux): the words and a few
// facts to show as tags. Anything they cannot read stays as it is (nil), and Item.Text always keeps the original.
// The formats are BotMux 2.50 and later (docs/protocol/protocol.md "BotMux messages"); spec/fixtures/botmux is
// shared with the web app.

const (
	botmuxOpen  = "<user_message>"
	botmuxClose = "</user_message>"
	// botmuxTaskOpen starts what an /api/trigger turn asks (botmux's buildExternalEventApplicationContext): the
	// node's own sends to a botmux session arrive that way (node/hostbotmux.go Prompt).
	botmuxTaskOpen  = `<botmux_task trusted="true">`
	botmuxTaskClose = "</botmux_task>"
)

// botmuxBlockRe is one top-level block: <tag …>…</tag> or <tag … />. Its name is in group 1 or 3.
var botmuxBlockRe = lazyre.New(`(?s)<([a-z][a-z0-9_]*)\b[^<>]*>.*?</([a-z][a-z0-9_]*)>|<([a-z][a-z0-9_]*)\b[^<>]*/>`)

var (
	botmuxSenderRe   = lazyre.New(`<sender\b([^<>]*)/>`)
	botmuxMentionRe  = lazyre.New(`<mention\b([^<>]*)/>`)
	botmuxAttachRe   = lazyre.New(`(?s)<attachments\b[^<>]*>(.*?)</attachments>`)
	botmuxAttachItem = lazyre.New(`<(?:image|file|video|audio)\s+n="`)
	botmuxLocationRe = lazyre.New(`<message_location\b([^<>]*)/>`)
	botmuxAttrRe     = lazyre.New(`([a-z_]+)="([^"]*)"`)
	// botmuxFormattingTail is the formatting block BotMux puts after the words when it sends them bare (its
	// replyDelivery "transcript" with one person in the chat): no <user_message> then.
	botmuxFormattingTail = lazyre.New(`(?s)\s*<botmux_formatting>.*?</botmux_formatting>\s*$`)
	// botmuxQuoteHint and botmuxTopicHint are the lines BotMux starts the words with when the message quoted another
	// one, and on the first turn of a topic that had messages before (its prompt.quote_hint, prompt.topic_context),
	// in Chinese and English.
	botmuxQuoteHint = lazyre.New(`^\s*\[(?:用户引用了消息 用 botmux quoted \S+ 查看|User quoted a message — run ` + "`botmux quoted [^`]+`" + ` to view it)\]\s*\n?`) // i18n-ignore: matches BotMux's text
	botmuxTopicHint = lazyre.New(`^\s*\[(?:本条是话题内的回复，[^\n]*|This is a reply inside a topic that already had prior messages[^\n]*)\]\s*\n?`)                 // i18n-ignore: matches BotMux's text
	botmuxSourceRe  = lazyre.New(`"sourceName":\s*("(?:[^"\\]|\\.)*")`)
)

// BotmuxMessage reads a user line BotMux wrote: nil when s is not one.
func BotmuxMessage(s string) *proto.Botmux {
	if !strings.Contains(s, "<user_message>") && !strings.Contains(s, "<botmux_") {
		return nil // most lines: no copy, no regexp
	}
	t := strings.TrimSpace(s)
	b := &proto.Botmux{Kind: proto.BotmuxMessage}
	var body string
	switch open := strings.Index(t, botmuxOpen); {
	case open >= 0:
		rest := t[open+len(botmuxOpen):]
		end := strings.Index(rest, botmuxClose)
		if end < 0 { // clipped inside the words: what there is of them
			body, b.Truncated = rest, true
			if !botmuxOnlyBlocks(t[:open]) {
				return nil
			}
		} else {
			body = rest[:end]
			if !botmuxOnlyBlocks(t[:open]) || !botmuxOnlyBlocks(rest[end+len(botmuxClose):]) {
				return nil // something else that happens to use the tag
			}
		}
		botmuxFacts(b, t[:open]+rest[min(len(rest), max(end, 0)+len(botmuxClose)):])
	case strings.HasPrefix(t, botmuxTaskOpen): // a trigger turn in BotMux's hook mode: its blocks go to the hook
		body = t
	case botmuxFormattingTail.MatchString(t): // bare: the words, then the formatting block
		body = botmuxFormattingTail.ReplaceAllString(t, "")
	default:
		return nil
	}
	if task, ok := botmuxTask(body); ok {
		b.Kind, b.Text = proto.BotmuxTrigger, task
		if m := botmuxSourceRe.FindStringSubmatch(body); m != nil {
			_ = json.Unmarshal([]byte(m[1]), &b.Source)
		}
		return b
	}
	body = strings.TrimSpace(body)
	for {
		if loc := botmuxQuoteHint.FindStringIndex(body); loc != nil {
			b.Quoted, body = true, body[loc[1]:]
		} else if loc := botmuxTopicHint.FindStringIndex(body); loc != nil {
			b.TopicContext, body = true, body[loc[1]:]
		} else {
			break
		}
	}
	b.Text = strings.TrimSpace(body)
	if b.Text == "" && b.Attachments == 0 && !b.Quoted {
		return nil
	}
	return b
}

// botmuxOnlyBlocks: s has nothing but blocks (and blank space) outside them, as BotMux writes around the words.
func botmuxOnlyBlocks(s string) bool {
	return strings.TrimSpace(botmuxBlockRe.ReplaceAllString(s, "")) == ""
}

// botmuxTask is what a trigger turn asks, from its <botmux_task> block.
func botmuxTask(body string) (string, bool) {
	start := strings.Index(body, botmuxTaskOpen)
	if start < 0 {
		return "", false
	}
	rest := body[start+len(botmuxTaskOpen):]
	end := strings.Index(rest, botmuxTaskClose)
	if end < 0 {
		return strings.TrimSpace(rest), true
	}
	return strings.TrimSpace(rest[:end]), true
}

// botmuxFacts reads the tags' facts off the blocks around the words.
func botmuxFacts(b *proto.Botmux, blocks string) {
	if m := botmuxSenderRe.FindStringSubmatch(blocks); m != nil {
		a := botmuxAttrs(m[1])
		b.Sender, b.SenderType = a["name"], a["type"]
	}
	for _, m := range botmuxMentionRe.FindAllStringSubmatch(blocks, -1) {
		if name := botmuxAttrs(m[1])["name"]; name != "" {
			b.Mentions = append(b.Mentions, name)
		}
	}
	if m := botmuxAttachRe.FindStringSubmatch(blocks); m != nil {
		b.Attachments = len(botmuxAttachItem.FindAllStringIndex(m[1], -1))
	}
	if m := botmuxLocationRe.FindStringSubmatch(blocks); m != nil {
		b.Thread = botmuxAttrs(m[1])["placement"] == "thread"
	}
}

func botmuxAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range botmuxAttrRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	return out
}

// botmuxSendFlags are `botmux send`'s flags that take a value (BotMux 3.59 cli.ts); the others take none.
var botmuxSendFlags = map[string]bool{
	"--mention": true, "--quote": true, "--reply-to": true, "--response-kind": true, "--expected-link": true, "--as": true,
	"--plugin-card-action": true, "--chat-id": true, "--into": true, "--secretary-action": true, "--secretary-reason": true,
	"--layout": true, "--session-id": true, "--image": true, "--images": true, "--file": true, "--files": true,
	"--video": true, "--videos": true, "--video-cover": true, "--video-covers": true, "--card": true, "--card-file": true,
}

// botmuxSendMedia are the flags that attach files to a reply (several at once comma-separated).
var botmuxSendMedia = map[string]bool{
	"--image": true, "--images": true, "--file": true, "--files": true, "--video": true, "--videos": true,
}

// BotmuxSend reads a shell call that is `botmux send`: the reply it posted, nil when the command is anything else or
// its words cannot be told from the command alone (a file, stdin from another command, a substitution).
func BotmuxSend(command string) *proto.Botmux {
	if !strings.Contains(command, "botmux") || !strings.Contains(command, "send") {
		return nil
	}
	words, heredoc, rest, ok := shellWords(strings.TrimSpace(command))
	for ok && len(words) > 0 && words[0] == "cd" && rest != "" { // `cd <dir> && botmux send --files <relative path>`
		words, heredoc, rest, ok = shellWords(strings.TrimSpace(rest))
	}
	if !ok || len(words) < 2 || (words[0] != "botmux" && path.Base(words[0]) != "botmux") || words[1] != "send" {
		return nil
	}
	b := &proto.Botmux{Kind: proto.BotmuxReply}
	var text []string
	for i := 2; i < len(words); i++ {
		w := words[i]
		name, value, eq := strings.Cut(w, "=")
		switch {
		case w == "--content-file":
			return nil
		case botmuxSendFlags[name]:
			if !eq {
				if i+1 >= len(words) {
					return nil
				}
				i++
				value = words[i]
			}
			switch {
			case name == "--response-kind" && value == "final":
				b.Final = true
			case botmuxSendMedia[name]:
				b.Attachments += len(strings.Split(value, ","))
			}
		case w == "--voice":
			b.Voice = true
		case strings.HasPrefix(w, "--"):
		default:
			text = append(text, w)
		}
	}
	b.Text = strings.TrimSpace(strings.Join(text, " "))
	if b.Text == "" {
		b.Text = strings.TrimSpace(heredoc)
	}
	if b.Text == "" {
		return nil
	}
	return b
}

// shellWords splits the first simple command of s the way sh would: quotes, backslashes, $'…', output redirects and one
// here-document (its body in heredoc). What follows it (after ;, &&, ||, | or a new line, the here-document's body
// first) does not change its words; rest is the next command when it runs after this one (; && or a new line) and
// there is no here-document. ok is false when the words depend on running something (a substitution, a subshell,
// an input redirect) or a quote or here-document is not closed.
func shellWords(s string) (words []string, heredoc, rest string, ok bool) {
	var cur strings.Builder
	inWord, delim, strip := false, "", false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\n' || c == ';' || c == '|' || c == '&' && !strings.HasPrefix(s[i:], "&>"):
			// the first command ends here: its here-document's body starts on the next line
			flush()
			if delim == "" {
				switch {
				case strings.HasPrefix(s[i:], "&&"):
					return words, "", s[i+2:], true
				case c == ';' || c == '\n':
					return words, "", s[i+1:], true
				}
				return words, "", "", true // a pipe or ||: what comes next is not simply next
			}
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return nil, "", "", false
			}
			body, ok := heredocBody(s[i+nl+1:], delim, strip)
			return words, body, "", ok
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, "", "", false
			}
			cur.WriteString(s[i+1 : i+1+end])
			inWord = true
			i += end + 2
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			j := i + 2
			for ; j < len(s) && s[j] != '\''; j++ {
				if s[j] == '\\' && j+1 < len(s) {
					j++
					cur.WriteString(ansiEscape(s[j]))
					continue
				}
				cur.WriteByte(s[j])
			}
			if j >= len(s) {
				return nil, "", "", false
			}
			inWord = true
			i = j + 1
		case c == '"':
			j := i + 1
			for ; j < len(s) && s[j] != '"'; j++ {
				switch {
				case s[j] == '\\' && j+1 < len(s) && strings.IndexByte("\"\\$`\n", s[j+1]) >= 0:
					j++
					if s[j] != '\n' {
						cur.WriteByte(s[j])
					}
				case s[j] == '$' || s[j] == '`':
					return nil, "", "", false // a substitution: the words depend on running it
				default:
					cur.WriteByte(s[j])
				}
			}
			if j >= len(s) {
				return nil, "", "", false
			}
			inWord = true
			i = j + 1
		case c == '\\' && i+1 < len(s):
			if s[i+1] != '\n' {
				cur.WriteByte(s[i+1])
				inWord = true
			}
			i += 2
		case c == '<' && strings.HasPrefix(s[i:], "<<") && delim == "":
			flush()
			i += 2
			if i < len(s) && s[i] == '-' {
				strip = true
				i++
			}
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' {
				j++
			}
			delim = strings.Trim(s[i:j], `'"`)
			if delim == "" {
				return nil, "", "", false
			}
			i = j
		case c == '>':
			// an output redirect (2>&1, >/dev/null) changes where the command's output goes, not its words
			if inWord && (cur.String() == "1" || cur.String() == "2") {
				cur.Reset()
				inWord = false
			}
			flush()
			i++
			if i < len(s) && s[i] == '>' {
				i++
			}
			if i < len(s) && s[i] == '&' {
				i++
			}
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			j := i
			for j < len(s) && strings.IndexByte(" \t\n|;&<>()`$'\"", s[j]) < 0 {
				j++
			}
			if j == i {
				return nil, "", "", false
			}
			i = j
		case strings.IndexByte("<>()`$&", c) >= 0:
			if c == '$' && (i+1 >= len(s) || strings.IndexByte("({", s[i+1]) < 0 && !isNameByte(s[i+1])) {
				cur.WriteByte(c)
				inWord = true
				i++
				continue
			}
			return nil, "", "", false
		default:
			cur.WriteByte(c)
			inWord = true
			i++
		}
	}
	flush()
	if delim != "" {
		return nil, "", "", false // a here-document with no body
	}
	return words, "", "", true
}

// heredocBody is a here-document's lines up to its delimiter; ok is false when the delimiter never comes (a clipped
// command).
func heredocBody(s, delim string, strip bool) (string, bool) {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strip {
			l = strings.TrimLeft(l, "\t")
			lines[i] = l
		}
		if l == delim {
			return strings.Join(lines[:i], "\n"), true
		}
	}
	return "", false
}

func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// ansiEscape is $'…”s escape \c.
func ansiEscape(c byte) string {
	switch c {
	case 'n':
		return "\n"
	case 't':
		return "\t"
	case 'r':
		return "\r"
	}
	return string(c)
}

// botmuxCommand is a Bash call's command line, for BotmuxSend (Codex's commands are Bash calls here too, codex.go).
func botmuxCommand(name string, input json.RawMessage) string {
	if name != "Bash" {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return in.Command
}

// markBotmux sets Botmux on the items BotMux wrapped (user lines) or the agent's `botmux send` calls.
func markBotmux(items []proto.Item) {
	for i := range items {
		it := &items[i]
		switch {
		case it.Role == "user" && it.Kind == proto.KindText && it.From == "":
			it.Botmux = BotmuxMessage(it.Text)
		case it.Kind == proto.KindToolUse:
			if cmd := botmuxCommand(it.ToolName, it.ToolInput); cmd != "" {
				it.Botmux = BotmuxSend(cmd)
			}
		}
	}
}
