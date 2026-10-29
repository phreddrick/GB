package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// finding is one normalised golangci-lint issue.
type finding struct {
	Path        string // repository-relative, forward slashes
	Linter      string
	Message     string
	Decl        string // enclosing top-level declaration, e.g. "func (*State).doTurn", "var x", "file"
	Fingerprint string // whitespace-normalised source line
	Line        int    // informational only, not part of the key
}

// key is the identity of a finding: location by file and enclosing
// declaration, rule, message and the normalised statement. Two identical
// statements in different declarations are different findings; two identical
// statements in the same declaration are counted as duplicates of one key.
func (f finding) key() string {
	return f.Path + "\t" + f.Linter + "\t" + f.Message + "\t" + f.Decl + "\t" + f.Fingerprint
}

// report is a validated, normalised golangci-lint JSON report.
type report struct {
	findings []finding
}

// rawReport mirrors the parts of golangci-lint's JSON output we rely on.
// Pointers distinguish "absent" from "empty" so that a truncated or foreign
// JSON document cannot pass as a clean report.
type rawReport struct {
	Issues *[]rawIssue `json:"Issues"`
	Report *struct {
		Error    string `json:"Error"`
		Warnings []struct {
			Tag  string `json:"Tag"`
			Text string `json:"Text"`
		} `json:"Warnings"`
		Linters []struct {
			Name    string `json:"Name"`
			Enabled bool   `json:"Enabled"`
		} `json:"Linters"`
	} `json:"Report"`
}

type rawIssue struct {
	FromLinter  *string  `json:"FromLinter"`
	Text        *string  `json:"Text"`
	SourceLines []string `json:"SourceLines"`
	Pos         *struct {
		Filename *string `json:"Filename"`
		Line     int     `json:"Line"`
	} `json:"Pos"`
}

// loadReport reads and validates a golangci-lint JSON report. prefix is the
// repository-relative directory golangci-lint ran in (for example "go/"), so
// that issue paths become repository-relative; absolute paths are made
// relative to the current working directory's repository root when possible.
func loadReport(p, prefix string, required []string) (*report, error) {
	raw, err := os.ReadFile(p) //nolint:gosec // path comes from the command line of a CI tool
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, errors.New("report file is empty")
	}
	var rr rawReport
	// Whole-document unmarshalling: any token after the top-level object
	// (a stray "]", a second document) is a syntax error, trailing whitespace
	// is not.
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	if rr.Report == nil {
		return nil, errors.New("missing Report section (not a golangci-lint report, or truncated)")
	}
	if rr.Issues == nil {
		return nil, errors.New("missing Issues section (not a golangci-lint report, or truncated)")
	}
	if rr.Report.Error != "" {
		return nil, fmt.Errorf("golangci-lint reported an execution error: %s", rr.Report.Error)
	}
	for _, w := range rr.Report.Warnings {
		// Warnings such as a linter that failed to run mean partial analysis.
		if strings.Contains(strings.ToLower(w.Text), "fail") || strings.Contains(strings.ToLower(w.Text), "error") {
			return nil, fmt.Errorf("golangci-lint warning indicates incomplete analysis: [%s] %s", w.Tag, w.Text)
		}
	}
	enabled := map[string]bool{}
	for _, l := range rr.Report.Linters {
		if l.Enabled {
			enabled[l.Name] = true
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("report lists no enabled linters")
	}
	for _, name := range required {
		if name == "" {
			continue
		}
		if !enabled[name] {
			return nil, fmt.Errorf("required linter %q was not enabled in this run", name)
		}
	}
	rep := &report{}
	decls := newDeclIndex()
	for i, is := range *rr.Issues {
		if is.FromLinter == nil || is.Text == nil || is.Pos == nil || is.Pos.Filename == nil {
			return nil, fmt.Errorf("issue %d is missing FromLinter, Text or Pos.Filename", i)
		}
		if *is.FromLinter == "" || *is.Text == "" || *is.Pos.Filename == "" {
			return nil, fmt.Errorf("issue %d has an empty linter, message or filename", i)
		}
		decl, err := decls.lookup(*is.Pos.Filename, is.Pos.Line)
		if err != nil {
			// Fail closed: a finding whose enclosing declaration cannot be
			// determined cannot be matched against the baseline safely.
			return nil, fmt.Errorf("issue %d (%s:%d): cannot determine enclosing declaration: %w", i, *is.Pos.Filename, is.Pos.Line, err)
		}
		f := finding{
			Path:        normalisePath(*is.Pos.Filename, prefix),
			Linter:      *is.FromLinter,
			Message:     strings.TrimSpace(*is.Text),
			Decl:        decl,
			Fingerprint: fingerprint(is.SourceLines),
			Line:        is.Pos.Line,
		}
		rep.findings = append(rep.findings, f)
	}
	return rep, nil
}

// ---- enclosing declaration ------------------------------------------------

// declIndex parses Go files (paths as reported by golangci-lint, relative to
// the directory it ran in) and resolves a line to its enclosing top-level
// declaration. Line numbers are used only to find the declaration; they are
// not part of the finding identity, so formatting changes that move lines do
// not change keys unless they move a statement to another declaration.
type declIndex struct {
	fset  *token.FileSet
	files map[string][]declSpan
}

type declSpan struct {
	name       string
	start, end int
}

func newDeclIndex() *declIndex {
	return &declIndex{fset: token.NewFileSet(), files: map[string][]declSpan{}}
}

func (d *declIndex) lookup(file string, line int) (string, error) {
	spans, ok := d.files[file]
	if !ok {
		src, err := os.ReadFile(file) //nolint:gosec // file named by the lint report, relative to the module directory
		if err != nil {
			return "", err
		}
		f, err := parser.ParseFile(d.fset, file, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return "", err
		}
		for _, decl := range f.Decls {
			start := d.fset.Position(decl.Pos()).Line
			end := d.fset.Position(decl.End()).Line
			if doc := declDoc(decl); doc != nil {
				start = d.fset.Position(doc.Pos()).Line
			}
			spans = append(spans, declSpan{name: declName(decl), start: start, end: end})
		}
		d.files[file] = spans
	}
	for _, sp := range spans {
		if line >= sp.start && line <= sp.end {
			return sp.name, nil
		}
	}
	return "file", nil // package clause, imports, comments between declarations
}

func declDoc(decl ast.Decl) *ast.CommentGroup {
	switch x := decl.(type) {
	case *ast.FuncDecl:
		return x.Doc
	case *ast.GenDecl:
		return x.Doc
	}
	return nil
}

func declName(decl ast.Decl) string {
	switch x := decl.(type) {
	case *ast.FuncDecl:
		if x.Recv != nil && len(x.Recv.List) > 0 {
			return "func " + typeString(x.Recv.List[0].Type) + "." + x.Name.Name
		}
		return "func " + x.Name.Name
	case *ast.GenDecl:
		name := ""
		if len(x.Specs) > 0 {
			switch sp := x.Specs[0].(type) {
			case *ast.TypeSpec:
				name = sp.Name.Name
			case *ast.ValueSpec:
				if len(sp.Names) > 0 {
					name = sp.Names[0].Name
				}
			case *ast.ImportSpec:
				name = "imports"
			}
		}
		return x.Tok.String() + " " + name
	}
	return "decl"
}

func typeString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return "(*" + typeString(x.X) + ")"
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return typeString(x.X)
	case *ast.IndexListExpr:
		return typeString(x.X)
	case *ast.SelectorExpr:
		return typeString(x.X) + "." + x.Sel.Name
	}
	return "?"
}

// normalisePath returns a clean repository-relative path with forward slashes.
func normalisePath(p, prefix string) string {
	p = filepath.ToSlash(p)
	if filepath.IsAbs(p) {
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = filepath.ToSlash(rel)
			}
		}
	}
	p = strings.TrimPrefix(p, "./")
	prefix = strings.TrimSuffix(filepath.ToSlash(prefix), "/")
	if prefix != "" && !strings.HasPrefix(p, prefix+"/") && !filepath.IsAbs(p) {
		p = prefix + "/" + p
	}
	return path.Clean(p)
}

// fingerprint collapses the reported source lines to a whitespace-normalised
// string so that indentation or line-wrapping changes do not alter identity.
func fingerprint(lines []string) string {
	var parts []string
	for _, l := range lines {
		parts = append(parts, strings.Join(strings.Fields(l), " "))
	}
	return strings.Join(parts, " / ")
}

// ---- baseline file -------------------------------------------------------

const baselineHeader = "# lintdiff baseline v3: path<TAB>linter<TAB>message<TAB>enclosing-declaration<TAB>source-fingerprint (one line per occurrence; regenerate with lintdiff -write-baseline)"

type baseline struct {
	counts map[string]int
	total  int
}

func loadBaseline(p string) (*baseline, error) {
	raw, err := os.ReadFile(p) //nolint:gosec // path comes from the command line of a CI tool
	if err != nil {
		return nil, err
	}
	b := &baseline{counts: map[string]int{}}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# lintdiff baseline v3") {
		return nil, errors.New("baseline header missing or not version 3; regenerate with -write-baseline")
	}
	for n, line := range lines[1:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" {
			return nil, fmt.Errorf("baseline line %d is malformed (expected 5 tab-separated fields): %q", n+2, line)
		}
		f := finding{Path: path.Clean(filepath.ToSlash(fields[0])), Linter: fields[1], Message: fields[2], Decl: fields[3], Fingerprint: fields[4]}
		b.counts[f.key()]++
		b.total++
	}
	if b.total == 0 {
		return nil, errors.New("baseline contains no findings; refusing to treat an empty baseline as authoritative (delete the file and the -baseline flag instead)")
	}
	return b, nil
}

func writeBaseline(p string, rep *report) error {
	keys := make([]string, 0, len(rep.findings))
	for _, f := range rep.findings {
		keys = append(keys, f.key())
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(baselineHeader + "\n")
	for _, k := range keys {
		b.WriteString(k + "\n")
	}
	return os.WriteFile(p, []byte(b.String()), 0o600)
}

// ---- comparison ----------------------------------------------------------

type result struct {
	baseTotal, repTotal int
	fresh               []string // new findings with location
	stale               []string // baseline keys that no longer occur (one line per key)
	staleCount          int      // stale occurrences (a key may allow several)
	matched             int
}

func compare(base *baseline, rep *report) result {
	r := result{baseTotal: base.total, repTotal: len(rep.findings)}
	seen := map[string]int{}
	for _, f := range rep.findings {
		k := f.key()
		seen[k]++
		if seen[k] > base.counts[k] {
			r.fresh = append(r.fresh, fmt.Sprintf("%s:%d: %s (%s) in %s [%s]", f.Path, f.Line, f.Message, f.Linter, f.Decl, f.Fingerprint))
		} else {
			r.matched++
		}
	}
	for k, n := range base.counts {
		if seen[k] < n {
			r.staleCount += n - seen[k]
			r.stale = append(r.stale, fmt.Sprintf("%s (missing %d of %d)", strings.ReplaceAll(k, "\t", " | "), n-seen[k], n))
		}
	}
	sort.Strings(r.fresh)
	sort.Strings(r.stale)
	return r
}

func (r result) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lintdiff: baseline %d findings, report %d findings, matched %d, new %d, stale %d\n",
		r.baseTotal, r.repTotal, r.matched, len(r.fresh), r.staleCount)
	if len(r.fresh) > 0 {
		b.WriteString("NEW findings (not in the baseline; fix them, or if they are accepted legacy debt add them with a reviewed baseline update):\n")
		for _, l := range r.fresh {
			b.WriteString("  " + l + "\n")
		}
	}
	if len(r.stale) > 0 {
		b.WriteString("STALE baseline entries (finding no longer occurs; shrink the baseline with -write-baseline so the allowance cannot be reused):\n")
		for _, l := range r.stale {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}
