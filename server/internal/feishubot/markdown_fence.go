package feishubot

import "strings"

// CodeFence tracks backtick or tilde fences in the model's Markdown. As with
// channel.Split, a fence has at most three leading spaces. Its closing run must
// match the opening character and be at least as long, with only spaces or tabs after it.
type CodeFence struct{ marker string }

// Line advances the fence state and reports whether line belongs to a fenced
// block, including its opening and closing lines. The zero value is ready to use.
func (f *CodeFence) Line(line string) bool {
	inside := f.marker != ""
	t := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	indent := len(t) - len(strings.TrimLeft(t, " "))
	if indent > 3 {
		return inside
	}
	t = t[indent:]
	marker := fenceMarker(t)
	if marker == "" {
		return inside
	}
	if !inside {
		f.marker = marker
		return true
	}
	if marker[0] == f.marker[0] && len(marker) >= len(f.marker) && strings.Trim(t[len(marker):], " \t") == "" {
		f.marker = ""
	}
	return true
}
