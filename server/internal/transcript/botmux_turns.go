package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// BotMux structured turn endpoint contract (deepcoldy/botmux#3597, #3599):
// GET /api/sessions/:sessionId/turns?since=<cursor>&limit=<n> returns BotmuxTurnsResponse.
const (
	BotmuxTurnsSchema   = "botmux.session-turns.v1"
	BotmuxCapOwnerActor = "ownerActor"
)

// BotmuxTurnsResponse is the JSON payload of BotMux's read-only GET /api/sessions/:id/turns endpoint.
type BotmuxTurnsResponse struct {
	OK           bool         `json:"ok"`
	Schema       string       `json:"schema"`
	Capabilities []string     `json:"capabilities"`
	Next         string       `json:"next"`
	Turns        []BotmuxTurn `json:"turns"`
}

// BotmuxTurn is one structured turn record from BotMux.
type BotmuxTurn struct {
	TurnID  string              `json:"turnId"`
	Seq     int64               `json:"seq"`
	At      int64               `json:"at"`
	Inbound []BotmuxTurnInbound `json:"inbound"`
	Replies []BotmuxTurnReply   `json:"replies"`
	Anchor  *BotmuxTurnAnchor   `json:"anchor,omitempty"`
}

// BotmuxTurnInbound is one inbound message folded into a BotMux turn.
type BotmuxTurnInbound struct {
	MessageID       string                 `json:"messageId"`
	Text            string                 `json:"text"`
	Sender          *BotmuxTurnSender      `json:"sender,omitempty"`
	Via             string                 `json:"via"` // "lark" | "trigger" | "owner_actor"
	QuotedMessageID string                 `json:"quotedMessageId,omitempty"`
	Attachments     []BotmuxTurnAttachment `json:"attachments,omitempty"`
	Mentions        []BotmuxTurnMention    `json:"mentions,omitempty"`
	Placement       *BotmuxTurnPlacement   `json:"placement,omitempty"`
}

// BotmuxTurnSender identifies the sender in BotMux's record. OpenID is parsed only so it can be ignored;
// only display names leave the node.
type BotmuxTurnSender struct {
	Type   string `json:"type"`
	OpenID string `json:"openId,omitempty"`
	Name   string `json:"name,omitempty"`
}

// BotmuxTurnAttachment is safe attachment metadata in BotMux's record.
type BotmuxTurnAttachment struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// BotmuxTurnMention is a mentioned user in BotMux's record. OpenID is never forwarded.
type BotmuxTurnMention struct {
	OpenID string `json:"openId,omitempty"`
	Name   string `json:"name,omitempty"`
}

// BotmuxTurnPlacement describes whether the message landed in a topic ("thread") or directly in the chat ("chat").
type BotmuxTurnPlacement struct {
	Kind          string `json:"kind"` // "thread" | "chat"
	RootMessageID string `json:"rootMessageId,omitempty"`
}

// BotmuxTurnReply is one confirmed reply posted by BotMux during the turn.
type BotmuxTurnReply struct {
	MessageID    string `json:"messageId"`
	ResponseKind string `json:"responseKind"`
	At           int64  `json:"at"`
	Text         string `json:"text,omitempty"`
}

// BotmuxTurnAnchor links a BotMux turn to the CLI transcript entry it was delivered to.
type BotmuxTurnAnchor struct {
	CLIID        string `json:"cliId"`
	CLISessionID string `json:"cliSessionId,omitempty"`
	InputSha256  string `json:"inputSha256"`
	CLIEntryID   string `json:"cliEntryId,omitempty"`
}

// HasCapability reports whether the turns response advertises cap.
func (r BotmuxTurnsResponse) HasCapability(cap string) bool {
	return r.OK && r.Schema == BotmuxTurnsSchema && slices.Contains(r.Capabilities, cap)
}

// ApplyBotmuxTurns overlays structured BotMux turn records onto transcript items.
// Each user turn is matched by anchor.cliEntryId first, then anchor.inputSha256.
// Matched turns populate Item.Botmux for the user message and for the turn's `botmux send` replies
// (including file/card replies whose text was not on the command line). Unmatched turns are left
// untouched so wrapper parsing from markBotmux acts as a per-turn fallback.
// Item.Text is never modified, and no open_id or messageId ever leaves the node.
func ApplyBotmuxTurns(items []proto.Item, turns []BotmuxTurn) []proto.Item {
	if len(items) == 0 || len(turns) == 0 {
		return items
	}
	byEntry := map[string]int{}
	byHash := map[string][]int{}
	for i := range turns {
		a := turns[i].Anchor
		if a == nil {
			continue
		}
		if id := strings.TrimSpace(a.CLIEntryID); id != "" {
			byEntry[id] = i
		}
		if h := strings.ToLower(strings.TrimSpace(a.InputSha256)); h != "" {
			byHash[h] = append(byHash[h], i)
		}
	}
	if len(byEntry) == 0 && len(byHash) == 0 {
		return items
	}

	used := make([]bool, len(turns))
	var out []proto.Item
	clone := func() []proto.Item {
		if out == nil {
			out = append([]proto.Item(nil), items...)
		}
		return out
	}

	// Find all user text item indices (turn boundaries).
	var userIdx []int
	for i := range items {
		it := &items[i]
		if it.Role == "user" && it.Kind == proto.KindText && it.From == "" {
			userIdx = append(userIdx, i)
		}
	}

	matchTurn := func(it proto.Item) int {
		if it.ID != "" {
			if idx, ok := byEntry[it.ID]; ok && !used[idx] {
				return idx
			}
			if prefix, _, found := strings.Cut(it.ID, "#"); found && prefix != "" {
				if idx, ok := byEntry[prefix]; ok && !used[idx] {
					return idx
				}
			}
		}
		for _, candidate := range inputHashCandidates(it.Text) {
			if idxs := byHash[candidate]; len(idxs) > 0 {
				for _, idx := range idxs {
					if !used[idx] {
						return idx
					}
				}
			}
		}
		return -1
	}

	for ui, start := range userIdx {
		tIdx := matchTurn(items[start])
		if tIdx < 0 {
			continue // per-turn fallback: keep wrapper-parsed Botmux as is
		}
		used[tIdx] = true
		turn := turns[tIdx]

		end := len(items)
		if ui+1 < len(userIdx) {
			end = userIdx[ui+1]
		}

		if bm := botmuxFromInbound(turn.Inbound, items[start].Botmux); bm != nil {
			dst := clone()
			dst[start].Botmux = bm
		}

		if len(turn.Replies) == 0 {
			continue
		}
		// Match structured replies (in order) to `botmux send` tool_use items in this turn.
		var sendIdx []int
		for j := start + 1; j < end; j++ {
			it := &items[j]
			if it.Kind != proto.KindToolUse {
				continue
			}
			if cmd := botmuxCommand(it.ToolName, it.ToolInput); cmd != "" && isBotmuxSendCommand(cmd) {
				sendIdx = append(sendIdx, j)
			}
		}
		for ri, reply := range turn.Replies {
			if ri >= len(sendIdx) {
				break
			}
			j := sendIdx[ri]
			if bm := botmuxFromReply(reply, items[j].Botmux); bm != nil {
				dst := clone()
				dst[j].Botmux = bm
			}
		}
	}

	if out == nil {
		return items
	}
	return out
}

func inputHashCandidates(text string) []string {
	if text == "" {
		return nil
	}
	h1 := sha256Hex(text)
	trimmed := strings.TrimRight(text, "\r\n")
	if trimmed == text {
		return []string{h1}
	}
	return []string{h1, sha256Hex(trimmed)}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func botmuxFromInbound(inbound []BotmuxTurnInbound, existing *proto.Botmux) *proto.Botmux {
	if len(inbound) == 0 {
		return nil
	}
	b := &proto.Botmux{Kind: proto.BotmuxMessage}
	allTrigger := true
	var parts []string
	var mentions []string
	var meta []proto.BotmuxAttachment
	for _, m := range inbound {
		if m.Via != "trigger" {
			allTrigger = false
		}
		if t := strings.TrimSpace(m.Text); t != "" {
			parts = append(parts, t)
		}
		if b.Sender == "" && m.Sender != nil {
			// Display name only: never fall back to openId.
			if name := strings.TrimSpace(m.Sender.Name); name != "" {
				b.Sender = name
				b.SenderType = strings.TrimSpace(m.Sender.Type)
			}
		}
		if strings.TrimSpace(m.QuotedMessageID) != "" {
			b.Quoted = true
		}
		if m.Placement != nil && m.Placement.Kind == "thread" {
			b.Thread = true
		}
		for _, a := range m.Attachments {
			name := strings.TrimSpace(a.Name)
			if name != "" {
				name = path.Base(strings.ReplaceAll(name, "\\", "/"))
				if name == "." || name == "/" {
					name = ""
				}
			}
			meta = append(meta, proto.BotmuxAttachment{
				Kind:     strings.TrimSpace(a.Kind),
				Name:     name,
				MimeType: strings.TrimSpace(a.MimeType),
			})
		}
		for _, mn := range m.Mentions {
			// Display name only: never include openId.
			if name := strings.TrimSpace(mn.Name); name != "" && !slices.Contains(mentions, name) {
				mentions = append(mentions, name)
			}
		}
	}
	if allTrigger {
		b.Kind = proto.BotmuxTrigger
		if existing != nil && existing.Source != "" {
			b.Source = existing.Source
		} else {
			b.Source = "Persona"
		}
	}
	if existing != nil && existing.TopicContext {
		b.TopicContext = true
	}
	b.Text = strings.Join(parts, "\n\n")
	if len(meta) > 0 {
		b.Attachments = len(meta)
		b.AttachmentMeta = meta
	}
	if len(mentions) > 0 {
		b.Mentions = mentions
	}
	if b.Text == "" && b.Attachments == 0 && !b.Quoted {
		return nil
	}
	return b
}

func botmuxFromReply(r BotmuxTurnReply, existing *proto.Botmux) *proto.Botmux {
	text := strings.TrimSpace(r.Text)
	if text == "" && existing != nil {
		text = existing.Text
	}
	final := r.ResponseKind == "final"
	if r.ResponseKind == "" && existing != nil {
		final = existing.Final
	}
	var voice bool
	var attachments int
	if existing != nil {
		voice = existing.Voice
		attachments = existing.Attachments
	}
	if text == "" && !voice && attachments == 0 {
		return nil
	}
	return &proto.Botmux{
		Kind:        proto.BotmuxReply,
		Text:        text,
		Final:       final,
		Voice:       voice,
		Attachments: attachments,
	}
}

// isBotmuxSendCommand reports whether cmd invokes `botmux send` (excluding --help and --dry-run),
// including forms whose body is not on the command line (--content-file, --card-file, --card-json, pipes).
func isBotmuxSendCommand(cmd string) bool {
	if !strings.Contains(cmd, "botmux") {
		return false
	}
	for _, seg := range splitShellSegments(cmd) {
		words := splitShellWordsSimple(seg)
		// Skip leading KEY=VAL environment assignments.
		for len(words) > 0 && isEnvAssign(words[0]) {
			words = words[1:]
		}
		if len(words) < 2 || path.Base(words[0]) != "botmux" || words[1] != "send" {
			continue
		}
		if slices.Contains(words[2:], "--help") || slices.Contains(words[2:], "-h") || slices.Contains(words[2:], "--dry-run") {
			continue
		}
		return true
	}
	return false
}

// splitShellSegments splits a shell command on unquoted ;, &&, ||, |, and newlines.
func splitShellSegments(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				cur.WriteByte(c)
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '\\':
			cur.WriteByte(c)
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		case ';', '\n':
			if seg := strings.TrimSpace(cur.String()); seg != "" {
				out = append(out, seg)
			}
			cur.Reset()
		case '&', '|':
			if i+1 < len(s) && s[i+1] == c {
				i++
			}
			if seg := strings.TrimSpace(cur.String()); seg != "" {
				out = append(out, seg)
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if seg := strings.TrimSpace(cur.String()); seg != "" {
		out = append(out, seg)
	}
	return out
}

func splitShellWordsSimple(s string) []string {
	var words []string
	var cur strings.Builder
	var quote byte
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			inWord = true
		case '\\':
			inWord = true
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		case ' ', '\t', '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

func isEnvAssign(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := w[i]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
