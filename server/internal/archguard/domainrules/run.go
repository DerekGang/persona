package domainrules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Stats is what one run inspected: packages per layer and files parsed in full.
type Stats struct {
	Layers  map[string]int // packages per layer, undeclared included
	Parsed  int            // files parsed in full for the rules: the budget the PR that changes it records
	Counted int            // files parsed in full for the application counts only (Ratchets)
}

// Summary is the line the tests print: packages per layer and the files parsed in full.
func (s Stats) Summary() string {
	var parts []string
	for _, l := range []string{layerCore, layerShared, layerKit, layerAdapter, layerApp, layerSeam, layerLibrary,
		layerContract, layerTestkit} {
		parts = append(parts, itoa(s.Layers[l])+" "+l)
	}
	line := "domain layers: " + strings.Join(parts, ", ") + ", " + itoa(s.Layers[undeclared]) + " undeclared (frozen); " +
		itoa(s.Parsed) + " files fully parsed"
	if s.Counted > 0 {
		line += ", and " + itoa(s.Counted) + " more for the application counts"
	}
	return line
}

// Inspected says whether a run looked at anything: packages, and files it parsed in full.
func (s Stats) Inspected() bool {
	n := 0
	for _, c := range s.Layers {
		n += c
	}
	return n > 0 && s.Parsed > 0
}

// fullLayers are the layers whose packages are parsed in full: their own rules read their code.
func fullLayer(l string) bool {
	switch l {
	case layerCore, layerShared, layerKit, layerAdapter, layerApp, layerSeam:
		return true
	}
	return false
}

// Run is every rule over pkgs: it parses the declared domain and application packages in full, and of the other
// files those that import a core, shared kernel, kit or adapter, or hold a .Wire selector. With scope set (a
// package's directory under the root, as the hook runs it), only that package and the files that import it are
// parsed and checked.
func Run(pkgs []*Pkg, idx Index, scope string) ([]Finding, Stats, error) {
	st := Stats{Layers: map[string]int{}}
	for _, p := range pkgs {
		l := idx.layer(p.Path)
		if l == "" {
			l = undeclared
		}
		st.Layers[l]++
		if fullLayer(l) || scope != "" && p.Rel == scope {
			if err := p.Full(); err != nil {
				return nil, st, err
			}
		}
	}
	scoped := idx.pkg(idx.module + "/" + scope)
	for _, p := range pkgs {
		for _, f := range p.Files {
			if f.ast != nil {
				continue
			}
			parse := false
			if scope == "" {
				parse = f.wire
				for _, imp := range f.Imports {
					if l := idx.layer(imp.Path); core(l) || l == layerKit || l == layerAdapter {
						parse = true
					}
				}
			} else if scoped != nil {
				parse = p == scoped
				for _, imp := range f.Imports {
					parse = parse || imp.Path == scoped.Path
				}
			}
			if parse {
				if err := f.parse(); err != nil {
					return nil, st, err
				}
			}
		}
	}
	var all []Finding
	all = append(all, Layers(pkgs, idx)...)
	for _, p := range pkgs {
		if p.decls != nil {
			all = append(all, Core(p, idx)...)
		}
		all = append(all, Outside(p, idx)...)
	}
	st.Parsed = Parsed(pkgs)
	if scope != "" {
		var in []Finding
		for _, f := range all {
			if inScope(f, scope) {
				in = append(in, f)
			}
		}
		all = in
	}
	sortFindings(all)
	return all, st, nil
}

// inScope says whether a finding belongs to the package at scope: it counts against it, or is in its files.
func inScope(f Finding, scope string) bool {
	return f.Key == scope || dirOf(f.Pos.Filename) == scope
}

func dirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// sortFindings orders findings by file, line, rule and message.
func sortFindings(fs []Finding) {
	sortSlice(fs, func(a, b Finding) bool {
		switch {
		case a.Pos.Filename != b.Pos.Filename:
			return a.Pos.Filename < b.Pos.Filename
		case a.Pos.Line != b.Pos.Line:
			return a.Pos.Line < b.Pos.Line
		case a.Rule != b.Rule:
			return a.Rule < b.Rule
		}
		return a.Msg < b.Msg
	})
}

// strict are the rules that take no count: what they report fails at once, frozen lists aside.
func strict(rule string) bool {
	switch rule {
	case "core-hook", "core-importers", "out-rules-import", "adapter-no-application", "adapter-import":
		return true
	}
	return strings.HasPrefix(rule, "layer-")
}

// Tally is the counts by "<key> <rule>" that a baseline holds: findings of counted rules (edge rules and strict rules
// left out) and the app-* counts.
func Tally(findings []Finding, counts []Count) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		if f.Edge != "" || strict(f.Rule) {
			continue
		}
		out[f.Key+" "+f.Rule]++
	}
	for _, c := range counts {
		out[c.Key+" "+c.Rule] += c.N
	}
	return out
}

// A Verdict is what a run's findings come to against the lists: the failures, and the notices that say what can be
// tidied (a count under its baseline, a frozen name that left).
type Verdict struct {
	Failures []string
	Notices  []string
}

// lowerHint is how to lower the domain baseline.
const lowerHint = "python3 scripts/ci/lower_baselines.py --only domain-rules"

// Judge holds findings and counts to the lists: frozen names suppress what they name, counts may not rise above their
// baseline (or above the merge base's count, where base has one for a line that names no PR), and every other
// finding fails. pkgs names what exists, for the notices. scope limits it to one package, as the hook runs.
func Judge(findings []Finding, counts []Count, pkgs []*Pkg, lists Lists, base map[string]int, scope string) Verdict {
	var v Verdict
	in := func(key string) bool { return scope == "" || key == scope }
	if scope == "" {
		v.Failures = append(v.Failures, lists.Problems...)
	}
	// Frozen names: the undeclared packages and the legacy edges.
	usedEdges := map[string]bool{}
	groups := map[string][]Finding{}
	var order []string
	for _, f := range findings {
		switch {
		case f.Rule == "layer-declared":
			// A package with no //persona:layer line at all may be frozen by name; a malformed one may not.
			if _, ok := lists.Undeclared.Has(f.Edge); ok && f.Edge != "" {
				continue
			}
			v.Failures = append(v.Failures, f.String())
		case f.Edge != "":
			if _, ok := lists.Edges.Has(f.Edge); ok {
				usedEdges[f.Edge] = true
				continue
			}
			v.Failures = append(v.Failures, f.String())
		case strict(f.Rule):
			v.Failures = append(v.Failures, f.String())
		default:
			id := f.Key + " " + f.Rule
			if _, ok := groups[id]; !ok {
				order = append(order, id)
			}
			groups[id] = append(groups[id], f)
		}
	}
	now := Tally(findings, counts)
	byID := map[string]Count{}
	for _, c := range counts {
		id := c.Key + " " + c.Rule
		byID[id] = c
		if _, ok := groups[id]; !ok {
			order = append(order, id)
			groups[id] = nil
		}
	}
	entries := map[string]BaselineEntry{}
	for _, e := range lists.Baseline.Entries {
		entries[e.Key+" "+e.Rule] = e
		if strict(e.Rule) {
			v.Failures = append(v.Failures, baselineFile+" line "+itoa(e.Line)+": "+e.Rule+" takes no count: fix what it reports")
		}
	}
	sortStrings(order)
	for _, id := range order {
		key, rule, _ := strings.Cut(id, " ")
		if !in(key) {
			continue
		}
		n, e := now[id], entries[id]
		limit, why := e.Count, "baseline "+itoa(e.Count)
		if b, ok := base[id]; ok && len(e.PRs) == 0 && b < limit {
			limit, why = b, "merge base "+itoa(b)+", baseline "+itoa(e.Count)
		}
		if n <= limit {
			continue
		}
		if c, ok := byID[id]; ok {
			what := c.Key + " adds " + countWhat(rule) + " (count " + itoa(n) + " > " + itoa(limit) + ")"
			v.Failures = append(v.Failures, c.Pos.Filename+":1: "+message(rule, what)+" Counted: "+strings.Join(c.Names, ", "))
			continue
		}
		for _, f := range groups[id] {
			v.Failures = append(v.Failures, f.String())
		}
		v.Failures = append(v.Failures, key+" "+rule+": "+itoa(n)+" now, above its limit ("+why+"): the count only falls; fix the code the lines above name")
	}
	// What can be tidied.
	for _, e := range lists.Baseline.Entries {
		id := e.Key + " " + e.Rule
		if !in(e.Key) || len(e.PRs) > 0 || strict(e.Rule) {
			continue
		}
		if n := now[id]; n < e.Count {
			v.Notices = append(v.Notices, baselineFile+" line "+itoa(e.Line)+": "+id+" is "+itoa(n)+", under its baseline "+
				itoa(e.Count)+": lower it with "+lowerHint)
		}
	}
	if scope == "" {
		declared := map[string]string{}
		for _, p := range pkgs {
			declared[display(p)] = p.Layer
		}
		for _, e := range lists.Undeclared.Entries {
			layer, exists := declared[e.Name]
			switch {
			case exists && layer != "":
				v.Notices = append(v.Notices, e.Name+" declares "+layer+" now: delete line "+itoa(e.Line)+" of "+undeclaredFile)
			case !exists && len(e.PRs) == 0:
				v.Notices = append(v.Notices, e.Name+" is gone: delete line "+itoa(e.Line)+" of "+undeclaredFile)
			case !exists:
				v.Notices = append(v.Notices, e.Name+" is pending ("+strings.Join(e.PRs, " ")+"); delete line "+itoa(e.Line)+
					" of "+undeclaredFile+" once its PRs have all closed")
			}
		}
	}
	for _, e := range lists.Edges.Entries {
		pkg, _, _ := strings.Cut(e.Name, " ")
		if !in(pkg) || usedEdges[e.Name] {
			continue
		}
		v.Notices = append(v.Notices, e.Name+" no longer happens: delete line "+itoa(e.Line)+" of "+edgesFile)
	}
	return v
}

// countWhat is what an app-* count holds, for its message.
func countWhat(rule string) string {
	switch rule {
	case "app-exported-record":
		return "an exported-field record"
	case "app-named-primitive":
		return "a named primitive type"
	}
	return "a direct write of DoneAt or DoneBy"
}

// Check is the hook in every declared package's rules_test.go: the same rules as archguard's TestDomainLayers, with
// the same lists, for the package the test runs in and the files that import it.
//
//	func TestDomainRules(t *testing.T) { domainrules.Check(t) }
func Check(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, module, err := moduleRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	scope := filepath.ToSlash(rel)
	lists, err := ReadLists(filepath.Join(root, "internal", "archguard", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := Load(root, module)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewIndex(module, pkgs, lists.Contracts)
	if p := idx.pkg(module + "/" + scope); p == nil || p.Layer == "" {
		t.Fatalf("%s declares no layer: the hook runs in a package whose package comment has a //persona:layer line", scope)
	}
	findings, _, err := Run(pkgs, idx, scope)
	if err != nil {
		t.Fatal(err)
	}
	v := Judge(findings, nil, pkgs, lists, nil, scope)
	for _, n := range v.Notices {
		t.Log("notice: " + n)
	}
	for _, f := range v.Failures {
		t.Error(f)
	}
}

// moduleRoot is the directory of the go.mod above dir and its module path.
func moduleRoot(dir string) (string, string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		b, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					return d, strings.Trim(strings.TrimSpace(m), `"`), nil
				}
			}
			return "", "", os.ErrNotExist
		}
		if filepath.Dir(d) == d {
			return "", "", err
		}
	}
}
