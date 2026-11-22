package archguard

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
// (CI_BASE_SHA, which check-fast's diff mode sets to its merge base), a count may not rise above its count at the
// merge base either. It prints the packages it checked per layer and the files it parsed in full, which is its
// budget, and fails when it inspected nothing.
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
		lists = lowerDomainBaseline(t, lists, domainrules.Measured(findings, counts))
	}
	v := domainrules.Judge(findings, counts, pkgs, lists, domainBaseMeasure(t, lists), "")
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

// lowerDomainBaseline writes the baseline lowered to m, the tree now, and returns the lists with it.
func lowerDomainBaseline(t *testing.T, lists domainrules.Lists, m domainrules.Measure) domainrules.Lists {
	t.Helper()
	path := filepath.Join("testdata", "domain_layers", "baseline.txt")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lowered := lists.Baseline.Lowered(string(old), m)
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

// domainBaseMeasure is the measure at the merge base with CI_BASE_SHA of the counts the merge base's baseline has,
// nil when no base is given. It reads the tree as it is, with every .go file under server/ the change touched since
// the merge base (committed or not, untracked included) put back as it was there. A base that does not resolve is an
// error, never "no base".
func domainBaseMeasure(t *testing.T, lists domainrules.Lists) *domainrules.Measure {
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
	var tracked []string
	for _, path := range append(touched, untracked...) {
		rel, ok := strings.CutPrefix(path, "server/")
		if !ok || !strings.HasSuffix(rel, ".go") {
			continue
		}
		if slices.Contains(untracked, path) {
			gone[rel] = true
		} else {
			tracked = append(tracked, path)
		}
	}
	blobs := gitBlobs(t, mergeBase, tracked)
	for _, path := range tracked {
		rel := strings.TrimPrefix(path, "server/")
		if text, ok := blobs[path]; ok {
			changed[rel] = text
		} else {
			gone[rel] = true // the change added it
		}
	}
	pkgs, err := domainrules.LoadOver(serverDir, serverModule, changed, gone)
	if err != nil {
		t.Fatal(err)
	}
	findings, counts, _ := domainRun(t, pkgs, lists)
	m := domainrules.Measured(findings, counts)
	// A count the merge base's baseline does not have is the change's own, and only the baseline holds it.
	held := domainrules.Measure{Counts: map[string]int{}, Names: m.Names}
	if text, err := exec.Command("git", "-C", serverDir, "show",
		mergeBase+":server/internal/archguard/testdata/domain_layers/baseline.txt").Output(); err == nil {
		for _, e := range domainrules.ParseLists("", "", "", string(text)).Baseline.Entries {
			held.Counts[e.ID()] = m.Counts[e.ID()]
		}
	}
	return &held
}

// gitBlobs is the text of each path (from the repository's root) at revision, read in one git cat-file --batch, so a
// change of many files costs one process; a path the revision does not have has no entry.
func gitBlobs(t *testing.T, revision string, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if len(paths) == 0 {
		return out
	}
	var in strings.Builder
	for _, p := range paths {
		in.WriteString(revision + ":" + p + "\n")
	}
	cmd := exec.Command("git", "-C", serverDir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(in.String())
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("git cat-file --batch at %s: %v", revision, err)
	}
	for _, p := range paths {
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			t.Fatalf("git cat-file --batch at %s: no answer for %s", revision, p)
		}
		header := string(data[:nl])
		data = data[nl+1:]
		if strings.HasSuffix(header, " missing") {
			continue
		}
		fields := strings.Fields(header)
		size, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil || len(fields) != 3 || fields[1] != "blob" || size+1 > len(data) {
			t.Fatalf("git cat-file --batch at %s: %q for %s", revision, header, p)
		}
		out[p] = string(data[:size])
		data = data[size+1:]
	}
	return out
}

// domainLayerNames is for scripts/test/name_freezes.py, which needs what the domain rules read at other commits (an
// open PR's head, its merge base) read exactly as TestDomainLayers reads it. Given a directory, TestDomainLayerNames
// reads each directory under it as a tree of the server's files and writes the undeclared package directories it
// finds there to <dir>/<name>.names, one per line; the tree's counts, with the types behind the ones an allowance can
// name, to <dir>/<name>.counts (domainrules.Measure's Lines); and the findings no count holds (an import a frozen list
// may hold, or a rule that fails at once) to <dir>/<name>.others, "<rule>\t<frozen name or message>". With
// -domain-layer-self it writes the server's own counts to <dir>/self.counts too. It checks nothing; without the flag
// it is skipped.
var (
	domainLayerNames = flag.String("domain-layer-names", "",
		"write the undeclared packages and the counts of each tree under this directory (scripts/test/name_freezes.py)")
	domainLayerSelf = flag.Bool("domain-layer-self", false, "with -domain-layer-names, also write the server's own counts")
)

func TestDomainLayerNames(t *testing.T) {
	dir := *domainLayerNames
	if dir == "" {
		t.Skip("only with -domain-layer-names (scripts/test/name_freezes.py)")
	}
	lists, err := domainrules.ReadLists("testdata")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, lines []string) {
		t.Helper()
		text := strings.Join(lines, "\n")
		if text != "" {
			text += "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	measure := func(pkgs []*domainrules.Pkg) (lines, others []string, err error) {
		idx := domainrules.NewIndex(serverModule, pkgs, lists.Contracts)
		findings, _, err := domainrules.Run(pkgs, idx, "")
		if err != nil {
			return nil, nil, err
		}
		counts, err := domainrules.Ratchets(pkgs, idx)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range findings {
			switch {
			case f.Rule == "layer-declared" && f.Edge != "": // the undeclared packages' freeze
			case f.Edge != "":
				others = append(others, f.Rule+"\t"+f.Edge)
			case domainrules.Strict(f.Rule):
				others = append(others, f.Rule+"\t"+f.Msg)
			}
		}
		slices.Sort(others)
		return domainrules.Measured(findings, counts).Lines(), others, nil
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
		write(e.Name()+".names", domainrules.Undeclared(pkgs))
		if lines, others, err := measure(pkgs); err != nil {
			t.Logf("%s: %v", e.Name(), err)
		} else {
			write(e.Name()+".counts", lines)
			write(e.Name()+".others", others)
		}
	}
	if *domainLayerSelf {
		pkgs, err := domainrules.Load(serverDir, serverModule)
		if err != nil {
			t.Fatal(err)
		}
		lines, _, err := measure(pkgs)
		if err != nil {
			t.Fatal(err)
		}
		write("self.counts", lines)
	}
}
