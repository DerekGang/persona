package feishubot

import (
	"strings"
	"testing"
)

func TestCardMarkdownKeepsFenceLookalikesVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, open, lookalike, close string }{
		{"short backticks", "````html", "```", "````"},
		{"trailing text", "````html", "```` still code", "````"},
		{"indented closer", "```html", "    ```", "```"},
		{"short tildes", "~~~~html", "~~~", "~~~~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := tc.open + "\nfirst line\n" + tc.lookalike + "\n\n<sample>literal code</sample>\n" + tc.close
			got := CardMarkdown(code + "\n\nOutside <sample>.")
			want := code + "\n\nOutside &#60;sample>."
			if got != want {
				t.Fatalf("code changed or prose stopped escaping:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestCardMarkdownIndentedFenceTextIsNotAnOpener(t *testing.T) {
	for _, indent := range []string{"    ", "\t"} {
		in := indent + "```example\n\nOutside <sample>."
		if got := CardMarkdown(in); !strings.Contains(got, "Outside &#60;sample>.") {
			t.Fatalf("indented text swallowed prose: %q", got)
		}
	}
}
