package domainrules

import (
	"os"
	"path/filepath"
	"strings"
)

// Lists is what the rules read besides the code, from archguard/testdata: the contract packages, and the three lists
// that hold the code that predates the rules (docs/operations/testing.md "Count ratchets").
type Lists struct {
	Contracts  []string // contracts.txt: paths under the module, or another module's import paths
	Undeclared Names    // domain_layers/undeclared.txt: the packages frozen without a layer
	Edges      Names    // domain_layers/legacy_edges.txt: imports frozen by name, "<pkg> imports <path>"
	Baseline   Baseline // domain_layers/baseline.txt: shrink-only counts, "<key> <rule> <count> [#PR ...]"
	Problems   []string // what is wrong with the files themselves
}

// The lists' files, under archguard/testdata.
const (
	contractsFile  = "contracts.txt"
	undeclaredFile = "domain_layers/undeclared.txt"
	edgesFile      = "domain_layers/legacy_edges.txt"
	baselineFile   = "domain_layers/baseline.txt"
)

// ReadLists reads the lists from dir, archguard's testdata directory.
func ReadLists(dir string) (Lists, error) {
	var l Lists
	text := map[string]string{}
	for _, name := range []string{contractsFile, undeclaredFile, edgesFile, baselineFile} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return l, err
		}
		text[name] = string(b)
	}
	return ParseLists(text[contractsFile], text[undeclaredFile], text[edgesFile], text[baselineFile]), nil
}

// ParseLists reads the lists from their text.
func ParseLists(contracts, undeclaredText, edges, baseline string) Lists {
	var l Lists
	for _, line := range strings.Split(contracts, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			l.Contracts = append(l.Contracts, line)
		}
	}
	var problems []string
	l.Undeclared, problems = ReadNames(undeclaredText)
	for _, p := range problems {
		l.Problems = append(l.Problems, undeclaredFile+": "+p)
	}
	l.Edges, problems = ReadNames(edges)
	for _, p := range problems {
		l.Problems = append(l.Problems, edgesFile+": "+p)
	}
	l.Baseline, problems = ReadBaseline(baseline)
	for _, p := range problems {
		l.Problems = append(l.Problems, baselineFile+": "+p)
	}
	return l
}

// Names is a freeze list's entries (docs/operations/testing.md "Count ratchets", the third kind): the names a list had
// when it was frozen, then the pending ones, each with the open PRs that add it. scripts/test/name_freezes.py writes
// the pending part, the format archguard's central lists use.
type Names struct{ Entries []NameEntry }

// A NameEntry is one line of a freeze list: its line number, the name and, for a pending one, its PRs.
type NameEntry struct {
	Line int
	Name string
	PRs  []string
}

// Has is the entry for name, and whether the list has one.
func (n Names) Has(name string) (NameEntry, bool) {
	for _, e := range n.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return NameEntry{}, false
}

// ReadNames reads a freeze list: comment lines start with #; an entry is a name, a pending one followed by its PRs
// (NAME #123 #456); names stay sorted and unique within each part, and the pending ones come last.
func ReadNames(text string) (Names, []string) {
	var n Names
	var problems []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		k := len(fields)
		for k > 0 && prNumber(fields[k-1]) {
			k--
		}
		if k == 0 {
			problems = append(problems, "line "+itoa(i+1)+" has PRs but no name")
			continue
		}
		name := strings.Join(fields[:k], " ")
		n.Entries = append(n.Entries, NameEntry{i + 1, name, fields[k:]})
	}
	seen := map[string]int{}
	pending := -1
	for i, e := range n.Entries {
		if first, dup := seen[e.Name]; dup {
			problems = append(problems, "line "+itoa(e.Line)+" repeats "+e.Name+" (line "+itoa(first)+")")
		} else {
			seen[e.Name] = e.Line
		}
		switch {
		case len(e.PRs) > 0 && pending < 0:
			pending = i
		case len(e.PRs) == 0 && pending >= 0:
			problems = append(problems, "line "+itoa(e.Line)+" ("+e.Name+") has no PR after the pending entries: those come last")
		}
		if i > 0 && (pending < 0 || i > pending) && e.Name < n.Entries[i-1].Name {
			problems = append(problems, "line "+itoa(e.Line)+" ("+e.Name+") is out of order: it belongs before "+
				n.Entries[i-1].Name+" (keep the names, and the pending ones, sorted)")
		}
	}
	return n, problems
}

func prNumber(s string) bool {
	_, ok := atoi(strings.TrimPrefix(s, "#"))
	return strings.HasPrefix(s, "#") && ok
}

// Baseline is the shrink-only counts of code that predates the rules, one per package and rule.
type Baseline struct{ Entries []BaselineEntry }

// A BaselineEntry is one line: the package a count is kept against, the rule, the count and, for an allowance made
// the day the rules landed for an open PR's additions, the PRs it allows for.
type BaselineEntry struct {
	Line  int
	Key   string
	Rule  string
	Count int
	PRs   []string
}

// ReadBaseline reads baseline.txt: "<key> <rule> <count>" per line, optionally followed by PRs, sorted by key and rule.
func ReadBaseline(text string) (Baseline, []string) {
	var b Baseline
	var problems []string
	seen := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		n := 0
		ok := len(f) >= 3
		if ok {
			n, ok = atoi(f[2])
		}
		for _, pr := range f[min(3, len(f)):] {
			ok = ok && prNumber(pr)
		}
		if !ok {
			problems = append(problems, "line "+itoa(i+1)+" is not <key> <rule> <count> [#PR ...]: "+line)
			continue
		}
		if _, known := rules[f[1]]; !known {
			problems = append(problems, "line "+itoa(i+1)+" names no rule: "+f[1])
			continue
		}
		e := BaselineEntry{i + 1, f[0], f[1], n, f[3:]}
		id := e.Key + " " + e.Rule
		if first, dup := seen[id]; dup {
			problems = append(problems, "line "+itoa(e.Line)+" repeats "+id+" (line "+itoa(first)+")")
		}
		seen[id] = e.Line
		if len(b.Entries) > 0 {
			prev := b.Entries[len(b.Entries)-1]
			if id < prev.Key+" "+prev.Rule {
				problems = append(problems, "line "+itoa(e.Line)+" ("+id+") is out of order: keep the lines sorted")
			}
		}
		b.Entries = append(b.Entries, e)
	}
	return b, problems
}

// Lowered is the baseline's text with every count that fell set to its count now and every count gone to zero
// removed; lines naming PRs (allowances for open PRs' additions) and comments stay as they are. It never raises or
// adds: counts holds the counts now, by "<key> <rule>".
func (b Baseline) Lowered(text string, counts map[string]int) string {
	byLine := map[int]BaselineEntry{}
	for _, e := range b.Entries {
		byLine[e.Line] = e
	}
	lines := strings.Split(text, "\n")
	var out []string
	for i, line := range lines {
		e, ok := byLine[i+1]
		if !ok || len(e.PRs) > 0 {
			out = append(out, line)
			continue
		}
		now := counts[e.Key+" "+e.Rule]
		switch {
		case now == 0:
		case now < e.Count:
			out = append(out, e.Key+" "+e.Rule+" "+itoa(now))
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
