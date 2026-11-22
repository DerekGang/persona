package archguard

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/archguard/domainrules"
)

// serverModule is the server's module path, the root the domain rules read packages under.
const serverModule = "github.com/nwverse/persona/server"

// domainBaselineUpdate lowers testdata/domain_layers/baseline.txt to the counts now, never raising or adding a line
// (scripts/ci/lower_baselines.py runs it).
const domainBaselineUpdate = "DOMAIN_LAYERS_UPDATE"

// TestDomainLayers holds the whole server to the domain rules (docs/server/architecture.md "Domain model"): every
// package declares its layer or is frozen by name in testdata/domain_layers/undeclared.txt, each declaration is
// checked, the declared domain packages follow their layer's rules and carry the rules_test.go hook, and the code
// that predates the rules stays within testdata/domain_layers' shrink-only counts and frozen imports. With a base
// (CI_BASE_SHA), a count may not rise above its count at the merge base either. It prints the packages it checked
// per layer and the files it parsed in full, which is its budget, and fails when it inspected nothing.
func TestDomainLayers(t *testing.T) {
	lists, err := domainrules.ReadLists("testdata")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := domainrules.Load(serverDir, serverModule)
	if err != nil {
		t.Fatal(err)
	}
	findings, counts, st := domainRun(t, pkgs, lists)
	t.Log(st.Summary())
	if !st.Inspected() {
		t.Fatalf("the domain rules inspected nothing under %s: %s", serverDir, st.Summary())
	}
	if os.Getenv(domainBaselineUpdate) != "" {
		lists = lowerDomainBaseline(t, lists, domainrules.Tally(findings, counts))
	}
	v := domainrules.Judge(findings, counts, pkgs, lists, domainBaseTally(t, lists), "")
	for _, n := range v.Notices {
		t.Log("notice: " + n)
	}
	for _, f := range v.Failures {
		t.Error(f)
	}
}

// domainRun is every rule and count over pkgs.
func domainRun(t *testing.T, pkgs []*domainrules.Pkg, lists domainrules.Lists) ([]domainrules.Finding,
	[]domainrules.Count, domainrules.Stats) {
	t.Helper()
	idx := domainrules.NewIndex(serverModule, pkgs, lists.Contracts)
	findings, st, err := domainrules.Run(pkgs, idx, "")
	if err != nil {
		t.Fatal(err)
	}
	counts, err := domainrules.Ratchets(pkgs, idx)
	if err != nil {
		t.Fatal(err)
	}
	st.Counted = domainrules.Parsed(pkgs) - st.Parsed
	return findings, counts, st
}

// lowerDomainBaseline writes the baseline lowered to tally, the counts now, and returns the lists with it.
func lowerDomainBaseline(t *testing.T, lists domainrules.Lists, tally map[string]int) domainrules.Lists {
	t.Helper()
	path := filepath.Join("testdata", "domain_layers", "baseline.txt")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lowered := lists.Baseline.Lowered(string(old), tally)
	if lowered == string(old) {
		return lists
	}
	if err := os.WriteFile(path, []byte(lowered), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("lowered %s to the counts now", path)
	lists, err = domainrules.ReadLists("testdata")
	if err != nil {
		t.Fatal(err)
	}
	return lists
}

// domainBaseTally is the counts at the merge base with CI_BASE_SHA of the lines the merge base's baseline has, nil
// when no base is given. It reads the tree as it is, with every .go file under server/ the change touched since the
// merge base (committed or not, untracked included) put back as it was there. A base that does not resolve is an
// error, never "no base".
func domainBaseTally(t *testing.T, lists domainrules.Lists) map[string]int {
	t.Helper()
	base := os.Getenv("CI_BASE_SHA")
	if base == "" {
		return nil
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", serverDir}, args...)...).Output()
		if err != nil {
			msg := ""
			if ee, ok := err.(*exec.ExitError); ok {
				msg = string(ee.Stderr)
			}
			t.Fatalf("git %s: %v %s (CI_BASE_SHA=%s must name a commit with a merge base here)",
				strings.Join(args, " "), err, msg, base)
		}
		return string(out)
	}
	mergeBase := strings.TrimSpace(git("merge-base", "HEAD", base))
	changed, gone := map[string]string{}, map[string]bool{}
	touched := strings.Fields(git("diff", "--name-only", "--no-renames", mergeBase, "--", "."))
	untracked := strings.Fields(git("ls-files", "--others", "--exclude-standard", "--full-name", "--", "."))
	for _, path := range append(touched, untracked...) {
		rel, ok := strings.CutPrefix(path, "server/")
		if !ok || !strings.HasSuffix(rel, ".go") {
			continue
		}
		if !slices.Contains(untracked, path) {
			if text, err := exec.Command("git", "-C", serverDir, "show", mergeBase+":"+path).Output(); err == nil {
				changed[rel] = string(text)
				continue
			}
			if strings.TrimSpace(git("ls-tree", "--name-only", mergeBase, "--", "../"+path)) != "" {
				t.Fatalf("git show %s:%s failed, but the file is there: refusing to read the merge base without it",
					mergeBase, path)
			}
		}
		gone[rel] = true
	}
	pkgs, err := domainrules.LoadOver(serverDir, serverModule, changed, gone)
	if err != nil {
		t.Fatal(err)
	}
	findings, counts, _ := domainRun(t, pkgs, lists)
	tally := domainrules.Tally(findings, counts)
	// A line the merge base's baseline does not have is the change's own, and only the baseline holds it.
	held := map[string]int{}
	if text, err := exec.Command("git", "-C", serverDir, "show",
		mergeBase+":server/internal/archguard/testdata/domain_layers/baseline.txt").Output(); err == nil {
		for _, e := range domainrules.ParseLists("", "", "", string(text)).Baseline.Entries {
			held[e.Key+" "+e.Rule] = tally[e.Key+" "+e.Rule]
		}
	}
	return held
}

// domainLayerNames is for scripts/test/name_freezes.py, which needs the packages without a //persona:layer line at
// other commits (an open PR's head, its merge base) read exactly as TestDomainLayers reads them. Given a directory,
// TestDomainLayerNames reads each directory under it as a tree of the server's files and writes the undeclared
// package directories it finds there to <dir>/<name>.names, one per line. It checks nothing; without the flag it is
// skipped.
var domainLayerNames = flag.String("domain-layer-names", "",
	"write the undeclared packages of each tree under this directory (scripts/test/name_freezes.py)")

func TestDomainLayerNames(t *testing.T) {
	dir := *domainLayerNames
	if dir == "" {
		t.Skip("only with -domain-layer-names (scripts/test/name_freezes.py)")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgs, err := domainrules.Load(filepath.Join(dir, e.Name()), serverModule)
		if err != nil {
			t.Logf("%s: %v", e.Name(), err)
			continue
		}
		var names strings.Builder
		for _, n := range domainrules.Undeclared(pkgs) {
			names.WriteString(n + "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()+".names"), []byte(names.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
