package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file name may legally end in whitespace. Reading, renaming, moving to a folder and creating must act on the entry
// the client named, never on a trimmed neighbour (#4567).
func TestFileActionsKeepTrailingWhitespacePaths(t *testing.T) {
	n, home := writeNode(t)
	proj := filepath.Join(home, "proj")
	plain := filepath.Join(proj, "report")
	spaced := filepath.Join(proj, "report ")
	makeFile(t, plain, "plain")
	makeFile(t, spaced, "spaced")
	if fi, err := os.Stat(spaced); err != nil || !strings.HasSuffix(fi.Name(), " ") {
		t.Skip("this filesystem does not keep a trailing space in a name")
	}

	// Read: the spaced entry's own bytes, not its neighbour's.
	fc, err := n.readFile("~/proj/report ", "", 0, false)
	if err != nil {
		t.Fatalf("read spaced: %v", err)
	}
	if fc.Text != "spaced" {
		t.Fatalf("read spaced got %q, want the spaced file's bytes", fc.Text)
	}
	if fileText(t, plain) != "plain" {
		t.Fatal("reading the spaced file changed the plain one")
	}

	// Rename: only the spaced entry moves.
	out, err := editCall(t, n, "file.rename", map[string]any{"path": "~/proj/report ", "name": "renamed.txt"})
	if err != nil {
		t.Fatalf("rename spaced: %v", err)
	}
	if out["path"] != "~/proj/renamed.txt" {
		t.Fatalf("rename spaced returned %v", out["path"])
	}
	if fileText(t, filepath.Join(proj, "renamed.txt")) != "spaced" || fileText(t, plain) != "plain" {
		t.Fatal("rename touched the wrong entry")
	}

	// Move into a destination folder whose own name ends in whitespace.
	dest := filepath.Join(home, "dest")
	destSpaced := filepath.Join(home, "dest ")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destSpaced, 0o755); err != nil {
		t.Fatal(err)
	}
	makeFile(t, filepath.Join(proj, "moved.txt"), "moved")
	if _, err := editCall(t, n, "file.move", map[string]any{"paths": []string{"~/proj/moved.txt"}, "dir": "~/dest "}); err != nil {
		t.Fatalf("move to spaced dir: %v", err)
	}
	if fileText(t, filepath.Join(destSpaced, "moved.txt")) != "moved" {
		t.Fatal("move landed in the wrong destination")
	}
	if _, err := os.Stat(filepath.Join(dest, "moved.txt")); !os.IsNotExist(err) {
		t.Fatal("move wrote into the trimmed destination as well")
	}

	// Empty and whitespace-only inputs stay errors instead of resolving to the cwd or a neighbour.
	for _, bad := range []string{"", "   ", "\t"} {
		if _, err := n.readFile(bad, home, 0, false); err == nil {
			t.Fatalf("read %q was accepted", bad)
		}
		if _, err := editCall(t, n, "file.rename", map[string]any{"path": bad, "name": "x.txt"}); err == nil {
			t.Fatalf("rename %q was accepted", bad)
		}
	}
}
