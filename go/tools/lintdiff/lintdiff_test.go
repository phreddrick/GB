package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test fixture is a small Go tree written to a temp dir that the test
// chdirs into, because lintdiff resolves the enclosing declaration of each
// finding by parsing the reported file.
const srcA = `package a

import "os"

func openA() {
	f, _ := os.Open("a")
	defer f.Close()
	f.Close()
}

func openB() {
	g, _ := os.Open("b")
	g.Close()
}
`

const srcB = `package b

func conv(v int) uint8 {
	x := uint8(v)
	return x
}
`

func issue(linter, text, src, file string, line int) string {
	return `{"FromLinter":"` + linter + `","Text":"` + text + `","SourceLines":["` + src + `"],"Pos":{"Filename":"` + file + `","Line":` + itoa(line) + `,"Column":2}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

const closeMsg = "Error return value of `f.Close` is not checked"
const g115 = "G115: integer overflow conversion int -> uint8"
const reportTail = `],"Report":{"Linters":[{"Name":"errcheck","Enabled":true},{"Name":"staticcheck","Enabled":true},{"Name":"gosec","Enabled":true},{"Name":"govet","Enabled":true},{"Name":"unused"}]}}`

func goodReport() string {
	return `{"Issues":[` +
		issue("errcheck", closeMsg, "\\tdefer f.Close()", "internal/a/a.go", 7) + "," +
		issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 8) + "," +
		issue("errcheck", "Error return value of `g.Close` is not checked", "\\tg.Close()", "internal/a/a.go", 13) + "," +
		issue("gosec", g115, "\\tx := uint8(v)", "internal/b/b.go", 4) +
		reportTail
}

func fixtureTree(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for p, src := range map[string]string{"internal/a/a.go": srcA, "internal/b/b.go": srcB} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func baselineFrom(t *testing.T, reportJSON string) string {
	t.Helper()
	rep, err := loadReport(write(t, "r.json", reportJSON), "go/", nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := filepath.Join(t.TempDir(), "baseline.txt")
	if err := writeBaseline(p, rep); err != nil {
		t.Fatal(err)
	}
	return p
}

func runGate(t *testing.T, baselinePath, reportJSON string) (int, string) {
	t.Helper()
	rep, err := loadReport(write(t, "r.json", reportJSON), "go/", nil)
	if err != nil {
		return exitFatal, err.Error()
	}
	base, err := loadBaseline(baselinePath)
	if err != nil {
		return exitFatal, err.Error()
	}
	res := compare(base, rep)
	code := exitOK
	if len(res.fresh) > 0 || len(res.stale) > 0 {
		code = exitFindings
	}
	return code, res.summary()
}

func TestCleanComparisonPasses(t *testing.T) {
	fixtureTree(t)
	code, out := runGate(t, baselineFrom(t, goodReport()), goodReport())
	if code != exitOK || !strings.Contains(out, "new 0, stale 0") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestDeclarationsAreResolved(t *testing.T) {
	fixtureTree(t)
	rep, err := loadReport(write(t, "r.json", goodReport()), "go/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.findings[0].Decl != "func openA" || rep.findings[2].Decl != "func openB" || rep.findings[3].Decl != "func conv" {
		t.Fatalf("decls: %q %q %q", rep.findings[0].Decl, rep.findings[2].Decl, rep.findings[3].Decl)
	}
}

// Codex's GB0-04R reproduction: the baseline has f.Close() in openA; the new
// code removes it there and adds an identical f.Close() statement in openB.
// Same file, same linter, same message, same source text: must be new+stale.
func TestIdenticalStatementMovedToAnotherFunctionIsDetected(t *testing.T) {
	fixtureTree(t)
	base := baselineFrom(t, goodReport())
	moved := `{"Issues":[` +
		issue("errcheck", closeMsg, "\\tdefer f.Close()", "internal/a/a.go", 7) + "," +
		issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 13) + "," + // now inside openB
		issue("errcheck", "Error return value of `g.Close` is not checked", "\\tg.Close()", "internal/a/a.go", 13) + "," +
		issue("gosec", g115, "\\tx := uint8(v)", "internal/b/b.go", 4) +
		reportTail
	code, out := runGate(t, base, moved)
	if code != exitFindings || !strings.Contains(out, "new 1") || !strings.Contains(out, "stale 1") || !strings.Contains(out, "in func openB") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestIdenticalStatementsInDifferentFunctionsAreDistinct(t *testing.T) {
	fixtureTree(t)
	rep, _ := loadReport(write(t, "r.json", `{"Issues":[`+
		issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 8)+","+
		issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 13)+reportTail), "go/", nil)
	if rep.findings[0].key() == rep.findings[1].key() {
		t.Fatal("identical statements in different functions share a key")
	}
}

func TestDuplicateAddedToSameFunctionIsNew(t *testing.T) {
	fixtureTree(t)
	base := baselineFrom(t, goodReport())
	dup := strings.Replace(goodReport(), `],"Report"`, ","+issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 8)+`],"Report"`, 1)
	code, out := runGate(t, base, dup)
	if code != exitFindings || !strings.Contains(out, "new 1") || !strings.Contains(out, "stale 0") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestFormattingOnlyLineMovementIsNotNew(t *testing.T) {
	fixtureTree(t)
	base := baselineFrom(t, goodReport())
	// Reformat a.go: extra blank lines and indentation shift every line number
	// but keep every statement inside the same function.
	reformatted := strings.Replace(srcA, "func openA() {\n", "func openA() {\n\n\n", 1)
	reformatted = strings.ReplaceAll(reformatted, "\tdefer f.Close()", "\t\tdefer   f.Close()")
	if err := os.WriteFile("internal/a/a.go", []byte(reformatted), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := `{"Issues":[` +
		issue("errcheck", closeMsg, "\\t\\tdefer   f.Close()", "internal/a/a.go", 9) + "," +
		issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 10) + "," +
		issue("errcheck", "Error return value of `g.Close` is not checked", "\\tg.Close()", "internal/a/a.go", 15) + "," +
		issue("gosec", g115, "\\tx := uint8(v)", "internal/b/b.go", 4) +
		reportTail
	code, out := runGate(t, base, moved)
	if code != exitOK {
		t.Fatalf("formatting change produced findings: %s", out)
	}
}

func TestMovingAFindingToAnUnrelatedDeclarationIsDetected(t *testing.T) {
	fixtureTree(t)
	base := baselineFrom(t, goodReport())
	// the G115 conversion now reported inside openA instead of conv
	moved := strings.Replace(goodReport(), issue("gosec", g115, "\\tx := uint8(v)", "internal/b/b.go", 4), issue("gosec", g115, "\\tx := uint8(v)", "internal/a/a.go", 6), 1)
	code, out := runGate(t, base, moved)
	if code != exitFindings || !strings.Contains(out, "new 1") || !strings.Contains(out, "stale 1") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestGenuinelyNewFindingFails(t *testing.T) {
	fixtureTree(t)
	rep := strings.Replace(goodReport(), `],"Report"`, ","+issue("gosec", "G404: Use of weak random number generator", "\\treturn x", "internal/b/b.go", 5)+`],"Report"`, 1)
	code, out := runGate(t, baselineFrom(t, goodReport()), rep)
	if code != exitFindings || !strings.Contains(out, "new 1") || !strings.Contains(out, "go/internal/b/b.go:5") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestRemovedFindingIsStaleAndFails(t *testing.T) {
	fixtureTree(t)
	rep := strings.Replace(goodReport(), ","+issue("gosec", g115, "\\tx := uint8(v)", "internal/b/b.go", 4), "", 1)
	code, out := runGate(t, baselineFrom(t, goodReport()), rep)
	if code != exitFindings || !strings.Contains(out, "stale 1") || !strings.Contains(out, "new 0") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestAbsoluteAndRelativePathsAreEquivalent(t *testing.T) {
	fixtureTree(t)
	cwd, _ := os.Getwd()
	abs := filepath.ToSlash(filepath.Join(cwd, "internal/b/b.go"))
	rep := strings.Replace(goodReport(), `"Filename":"internal/b/b.go"`, `"Filename":"`+abs+`"`, 1)
	repRel, _ := loadReport(write(t, "r.json", goodReport()), "go/", nil)
	repAbs, err := loadReport(write(t, "r2.json", rep), "go/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if repRel.findings[3].key() != repAbs.findings[3].key() {
		t.Fatalf("keys differ: %q vs %q", repRel.findings[3].key(), repAbs.findings[3].key())
	}
	if !strings.HasPrefix(repAbs.findings[3].Path, "go/") {
		t.Fatalf("path not repository-relative: %q", repAbs.findings[3].Path)
	}
}

func TestUnresolvableFileIsFatal(t *testing.T) {
	fixtureTree(t)
	rep := strings.Replace(goodReport(), `"Filename":"internal/b/b.go"`, `"Filename":"internal/missing/m.go"`, 1)
	if _, err := loadReport(write(t, "r.json", rep), "go/", nil); err == nil {
		t.Fatal("finding in a file that cannot be parsed was accepted")
	}
}

// GB0-03R: whole-document validation.
func TestTrailingContentIsRejected(t *testing.T) {
	fixtureTree(t)
	good := goodReport()
	cases := map[string]struct {
		body string
		ok   bool
	}{
		"valid":                   {good, true},
		"trailing whitespace":     {good + "\n  \n", true},
		"trailing bracket":        {good + "]", false},
		"second document":         {good + ` {"Issues":[],"Report":{}}`, false},
		"second document newline": {good + "\n{}", false},
		"truncated":               {good[:len(good)/2], false},
	}
	for name, c := range cases {
		_, err := loadReport(write(t, "x.json", c.body), "go/", nil)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, wanted ok=%v", name, err, c.ok)
		}
	}
}

func TestMalformedAndForeignJSONAreFatal(t *testing.T) {
	fixtureTree(t)
	cases := map[string]string{
		"not json":         "this is not json",
		"empty file":       "",
		"empty object":     "{}",
		"issues only":      `{"Issues":[]}`,
		"report only":      `{"Report":{"Linters":[{"Name":"errcheck","Enabled":true}]}}`,
		"null issues":      `{"Issues":null,"Report":{"Linters":[{"Name":"errcheck","Enabled":true}]}}`,
		"issue w/o linter": `{"Issues":[{"Text":"x","Pos":{"Filename":"internal/a/a.go","Line":1}}],"Report":{"Linters":[{"Name":"errcheck","Enabled":true}]}}`,
		"issue w/o pos":    `{"Issues":[{"FromLinter":"errcheck","Text":"x"}],"Report":{"Linters":[{"Name":"errcheck","Enabled":true}]}}`,
	}
	for name, body := range cases {
		if _, err := loadReport(write(t, "x.json", body), "go/", nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReportErrorAndMissingLintersAreFatal(t *testing.T) {
	fixtureTree(t)
	withErr := `{"Issues":[],"Report":{"Error":"can't run linter govet: boom","Linters":[{"Name":"errcheck","Enabled":true}]}}`
	if _, err := loadReport(write(t, "e.json", withErr), "go/", []string{"errcheck"}); err == nil {
		t.Error("report with Error accepted")
	}
	noGosec := `{"Issues":[],"Report":{"Linters":[{"Name":"errcheck","Enabled":true},{"Name":"gosec"}]}}`
	if _, err := loadReport(write(t, "n.json", noGosec), "go/", []string{"errcheck", "gosec"}); err == nil {
		t.Error("report without the required gosec linter accepted")
	}
	warn := `{"Issues":[],"Report":{"Warnings":[{"Tag":"runner","Text":"linter gosec failed to run"}],"Linters":[{"Name":"errcheck","Enabled":true},{"Name":"gosec","Enabled":true}]}}`
	if _, err := loadReport(write(t, "w.json", warn), "go/", nil); err == nil {
		t.Error("report with a failure warning accepted")
	}
}

func TestGenuineZeroFindingReportIsValidButNotAgainstNonEmptyBaseline(t *testing.T) {
	fixtureTree(t)
	zero := `{"Issues":[],"Report":{"Linters":[{"Name":"errcheck","Enabled":true},{"Name":"staticcheck","Enabled":true},{"Name":"gosec","Enabled":true},{"Name":"govet","Enabled":true}]}}`
	rep, err := loadReport(write(t, "z.json", zero), "go/", []string{"errcheck", "staticcheck", "gosec", "govet"})
	if err != nil || len(rep.findings) != 0 {
		t.Fatalf("genuine zero report rejected: %v", err)
	}
	code, out := runGate(t, baselineFrom(t, goodReport()), zero)
	if code != exitFindings || !strings.Contains(out, "stale 4") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestBaselineValidation(t *testing.T) {
	if _, err := loadBaseline(write(t, "b.txt", "")); err == nil {
		t.Error("empty baseline accepted")
	}
	if _, err := loadBaseline(write(t, "b.txt", "# lintdiff baseline v2: old\ngo/x.go\terrcheck\tmsg\tfp\n")); err == nil {
		t.Error("old-format baseline accepted")
	}
	if _, err := loadBaseline(write(t, "b.txt", baselineHeader+"\nonly\ttwo\n")); err == nil {
		t.Error("malformed baseline line accepted")
	}
	if _, err := loadBaseline(write(t, "b.txt", baselineHeader+"\n")); err == nil {
		t.Error("header-only baseline accepted")
	}
	if _, err := loadBaseline(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Error("missing baseline accepted")
	}
}

func TestRunExitCodes(t *testing.T) {
	fixtureTree(t)
	devnull, _ := os.Open(os.DevNull)
	defer func() { _ = devnull.Close() }()
	if run(nil, devnull, devnull) != exitFatal {
		t.Error("usage error should be fatal")
	}
	bp := baselineFrom(t, goodReport())
	rp := write(t, "r.json", goodReport())
	if run([]string{"-baseline", bp, "-report", rp, "-prefix", "go/"}, devnull, devnull) != exitOK {
		t.Error("clean run should exit 0")
	}
	if run([]string{"-baseline", bp, "-report", write(t, "bad.json", "{"), "-prefix", "go/"}, devnull, devnull) != exitFatal {
		t.Error("malformed report should be fatal")
	}
	if run([]string{"-baseline", filepath.Join(t.TempDir(), "none"), "-report", rp, "-prefix", "go/"}, devnull, devnull) != exitFatal {
		t.Error("missing baseline should be fatal")
	}
}

// Documented residual limitation (GB0-04R, accepted by the owner for Phase 0):
// two identical statements inside the same declaration share a key, so moving
// one between blocks of that function, or swapping which of two identical
// lines carries the finding, is not detected. Cross-function moves, duplicates
// and new findings are. This test pins the behaviour so that any change to it
// is deliberate.
func TestSameDeclarationSwapIsNotDistinguished(t *testing.T) {
	fixtureTree(t)
	base := baselineFrom(t, goodReport())
	// the f.Close() finding reported on a different line of openA (same statement text)
	swapped := strings.Replace(goodReport(), issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 8), issue("errcheck", closeMsg, "\\tf.Close()", "internal/a/a.go", 6), 1)
	code, out := runGate(t, base, swapped)
	if code != exitOK {
		t.Fatalf("documented limitation changed: same-declaration move is now detected (%s); update docs/testing/lint-baseline.md", out)
	}
}
