package proto

import (
	"slices"
	"testing"
)

// Hidden folders use the blocked folders' syntax: the same cleaning, validity and matcher.
func TestAgentDirPatterns(t *testing.T) {
	for in, want := range map[string]string{" ~/work/ ": "~/work", "/opt/b//": "/opt/b", "*multiremi*": "*multiremi*"} {
		if got, ok := CleanDirPattern(in); !ok || got != want {
			t.Errorf("clean %q = %q %v", in, got, ok)
		}
	}
	for _, everything := range []string{"", "~", "~/", "/", "//", "~/.."} {
		if _, ok := CleanDirPattern(everything); ok {
			t.Errorf("%q covers everything", everything)
		}
	}
	rules := []HiddenAgentDir{{Pattern: "/work", Enabled: true}, {Pattern: "/off", Enabled: false}, {Pattern: "*multiremi*", Enabled: true}}
	patterns := HiddenAgentDirPatterns(rules)
	if !slices.Equal(patterns, []string{"/work", "*multiremi*"}) {
		t.Fatalf("enabled patterns %v", patterns)
	}
	for dir, want := range map[string]string{
		"/work/a/src": "/work", "/home/me/.remi/multiremi/workspaces/x": "*multiremi*", "/off/a": "", "/workshop": "",
	} {
		if got := DirPatternMatchedBy(dir, "/home/me", patterns); got != want {
			t.Errorf("%q matched by %q, want %q", dir, got, want)
		}
	}
	if got := ClaudeDirBlockedBy("/work/a", "", []string{"/work"}); got != DirPatternMatchedBy("/work/a", "", []string{"/work"}) {
		t.Fatal("the two lists match differently")
	}
}
