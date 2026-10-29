// Command lintdiff compares a golangci-lint JSON report against the committed
// static-analysis baseline (docs/testing/lint-baseline.txt) and fails when the
// report cannot be trusted, when it contains findings that are not in the
// baseline, or when the baseline contains findings that no longer occur.
//
// Finding identity (the "key") is: repository-relative path, linter, message,
// the enclosing top-level declaration (resolved by parsing the Go file with
// go/parser) and a whitespace-normalised fingerprint of the offending source
// line. Line numbers are used only to find the declaration and are not part
// of the key, so formatting-only movement does not create a new finding, while
// the same statement in another declaration or file does. Duplicates within
// one declaration are counted: the report may contain at most as many
// occurrences of a key as the baseline. Baseline keys that no longer occur
// are "stale" and fail the gate too, so resolved findings cannot leave
// reusable allowances; the baseline is then shrunk deliberately with
// -write-baseline and reviewed in the diff. Residual limitation: two
// occurrences of the same statement in the same declaration are not told
// apart; swapping one for the other inside a function is invisible and is
// accepted because the function's lint-relevant content is unchanged.
//
// Fail-closed rules: a missing or unparseable report or baseline, a report
// whose Report.Error is set, a report that does not list the expected linters
// as enabled, or a report missing the Issues/Report sections is a fatal error
// (exit 2), never "zero findings".
//
// Exit codes: 0 gate passed; 1 new or stale findings; 2 fatal (untrusted
// input or usage error).
//
// Usage:
//
//	go run ./tools/lintdiff -baseline ../docs/testing/lint-baseline.txt -report lint.json -prefix go/
//	go run ./tools/lintdiff -report lint.json -prefix go/ -write-baseline ../docs/testing/lint-baseline.txt
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	opts, err := parseFlags(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lintdiff:", err)
		return exitFatal
	}
	rep, err := loadReport(opts.reportPath, opts.prefix, opts.requiredLinters)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lintdiff: FATAL: report rejected:", err)
		return exitFatal
	}
	if opts.writeBaseline != "" {
		if err := writeBaseline(opts.writeBaseline, rep); err != nil {
			_, _ = fmt.Fprintln(stderr, "lintdiff: FATAL:", err)
			return exitFatal
		}
		_, _ = fmt.Fprintf(stdout, "lintdiff: wrote baseline with %d findings to %s\n", len(rep.findings), opts.writeBaseline)
		return exitOK
	}
	base, err := loadBaseline(opts.baselinePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lintdiff: FATAL: baseline rejected:", err)
		return exitFatal
	}
	res := compare(base, rep)
	_, _ = fmt.Fprint(stdout, res.summary())
	if len(res.fresh) > 0 || len(res.stale) > 0 {
		return exitFindings
	}
	return exitOK
}

const (
	exitOK       = 0
	exitFindings = 1
	exitFatal    = 2
)

type options struct {
	baselinePath    string
	reportPath      string
	prefix          string
	writeBaseline   string
	requiredLinters []string
}

func parseFlags(args []string) (options, error) {
	o := options{requiredLinters: []string{"errcheck", "staticcheck", "gosec", "govet"}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "-baseline":
			o.baselinePath, err = next()
		case "-report":
			o.reportPath, err = next()
		case "-prefix":
			o.prefix, err = next()
		case "-write-baseline":
			o.writeBaseline, err = next()
		case "-require-linters":
			var v string
			v, err = next()
			o.requiredLinters = strings.Split(v, ",")
		default:
			return o, fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	if o.reportPath == "" {
		return o, fmt.Errorf("-report is required")
	}
	if o.baselinePath == "" && o.writeBaseline == "" {
		return o, fmt.Errorf("one of -baseline or -write-baseline is required")
	}
	return o, nil
}
