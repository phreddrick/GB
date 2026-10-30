// gbctl is the operator's command-line client for the gb-server admin
// listener (Phase 1 skeleton).
//
// Usage:
//
//	gbctl [-server URL] [-timeout D] [-json] <command>
//
//	status                       show engine status
//	update                       run a full update now and wait for it
//	segment                      run a movement segment now
//	quiesce                      pause turns and mutating commands
//	resume                       resume a quiesced engine
//	universe init -seed N [-stars N] [-min-planets N] [-max-planets N]
//
// The server address comes from -server or GBCTL_SERVER (default
// http://127.0.0.1:8081). The admin token is never a command-line argument:
// set GBCTL_TOKEN, or GBCTL_TOKEN_FILE naming a file with the token (first
// line), and the tool never prints it.
//
// Exit status: 0 success; 1 usage; 2 cannot reach the server; 3 token
// rejected; 4 operation refused by the engine's state (halted, not serving,
// already initialised, bad parameters); 5 server error, timeout, or a reply
// that does not confirm success (malformed, truncated, empty, wrong
// operation, ok=false): success is printed only for a validated reply.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/admin"
	"github.com/phreddrick/GB/go/internal/db"
)

const (
	exitOK      = 0
	exitUsage   = 1
	exitConnect = 2
	exitAuth    = 3
	exitRefused = 4
	exitServer  = 5
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	fs := flag.NewFlagSet("gbctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultServer := "http://127.0.0.1:8081"
	if v, ok := lookupEnv("GBCTL_SERVER"); ok && v != "" {
		defaultServer = v
	}
	server := fs.String("server", defaultServer, "admin listener base URL (or GBCTL_SERVER)")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall request timeout")
	asJSON := fs.Bool("json", false, "print the raw JSON reply")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: gbctl [-server URL] [-timeout D] [-json] status|update|segment|quiesce|resume|universe init -seed N [-stars N] [-min-planets N] [-max-planets N]\n")
		_, _ = fmt.Fprintf(stderr, "token: GBCTL_TOKEN or GBCTL_TOKEN_FILE (never a flag)\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return exitUsage
	}

	token, err := tokenFromEnv(lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gbctl: %v\n", err)
		return exitUsage
	}
	client := &admin.Client{BaseURL: *server, Token: token, HTTP: &http.Client{Timeout: *timeout}}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var resp *admin.Response
	switch rest[0] {
	case "status":
		resp, err = client.Status(ctx)
	case "update":
		resp, err = client.Update(ctx)
	case "segment":
		resp, err = client.Segment(ctx)
	case "quiesce":
		resp, err = client.Quiesce(ctx)
	case "resume":
		resp, err = client.Resume(ctx)
	case "universe":
		if len(rest) < 2 || rest[1] != "init" {
			_, _ = fmt.Fprintf(stderr, "gbctl: usage: universe init -seed N [-stars N] [-min-planets N] [-max-planets N]\n")
			return exitUsage
		}
		ifs := flag.NewFlagSet("universe init", flag.ContinueOnError)
		ifs.SetOutput(stderr)
		seed := ifs.Int64("seed", 0, "generation seed (required, non-zero; record it)")
		stars := ifs.Int("stars", 0, "number of stars (default 100)")
		minP := ifs.Int("min-planets", 0, "minimum planets per system (default 2)")
		maxP := ifs.Int("max-planets", 0, "maximum planets per system (default 10)")
		if err := ifs.Parse(rest[2:]); err != nil {
			return exitUsage
		}
		if *seed == 0 {
			_, _ = fmt.Fprintf(stderr, "gbctl: universe init: -seed is required and must be non-zero\n")
			return exitUsage
		}
		resp, err = client.UniverseInit(ctx, admin.InitRequest{Seed: *seed, Stars: *stars, MinPlanets: *minP, MaxPlanets: *maxP})
	default:
		_, _ = fmt.Fprintf(stderr, "gbctl: unknown command %q\n", rest[0])
		fs.Usage()
		return exitUsage
	}

	if err != nil {
		var ae *admin.Error
		switch {
		case errors.Is(err, admin.ErrBadResponse):
			// Never report success on a reply that does not confirm it.
			_, _ = fmt.Fprintf(stderr, "gbctl: %s: %v\n", rest[0], redact(err.Error(), token))
			_, _ = fmt.Fprintf(stderr, "gbctl: the outcome is unknown; check with 'gbctl status' before retrying\n")
			return exitServer
		case errors.Is(err, admin.ErrUnauthorized):
			_, _ = fmt.Fprintf(stderr, "gbctl: the server rejected the admin token (check GBCTL_TOKEN / GBCTL_TOKEN_FILE against the server's GB_ADMIN_TOKEN)\n")
			return exitAuth
		case errors.As(err, &ae):
			_, _ = fmt.Fprintf(stderr, "gbctl: %v\n", ae)
			if resp != nil && resp.Status != nil {
				_, _ = fmt.Fprintf(stderr, "gbctl: engine mode %s\n", resp.Status.Mode)
			}
			switch ae.Code {
			case http.StatusConflict, http.StatusBadRequest:
				return exitRefused
			default:
				return exitServer
			}
		case errors.Is(err, context.DeadlineExceeded):
			_, _ = fmt.Fprintf(stderr, "gbctl: timed out after %s waiting for the server (the operation may still be running)\n", *timeout)
			return exitServer
		default:
			_, _ = fmt.Fprintf(stderr, "gbctl: cannot reach %s: %v\n", *server, redact(err.Error(), token))
			return exitConnect
		}
	}

	if rest[0] == "universe" {
		// Defence in depth (GB1-06R): the client has validated the record;
		// never print success without it.
		if _, ok := resp.Result.(*db.UniverseRecord); !ok {
			_, _ = fmt.Fprintf(stderr, "gbctl: universe: the reply did not carry a validated universe record\n")
			_, _ = fmt.Fprintf(stderr, "gbctl: the outcome is unknown; check with 'gbctl status' before retrying\n")
			return exitServer
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		return exitOK
	}
	printHuman(stdout, rest[0], resp)
	return exitOK
}

func tokenFromEnv(lookupEnv func(string) (string, bool)) (string, error) {
	direct, hasDirect := lookupEnv("GBCTL_TOKEN")
	file, hasFile := lookupEnv("GBCTL_TOKEN_FILE")
	switch {
	case hasDirect && direct != "" && hasFile && file != "":
		return "", errors.New("set only one of GBCTL_TOKEN and GBCTL_TOKEN_FILE")
	case hasDirect && direct != "":
		return direct, nil
	case hasFile && file != "":
		data, err := os.ReadFile(file) //nolint:gosec // G304: the operator names the token file on purpose
		if err != nil {
			return "", fmt.Errorf("GBCTL_TOKEN_FILE: %w", err)
		}
		tok := strings.TrimRight(strings.SplitN(string(data), "\n", 2)[0], "\r")
		if tok == "" {
			return "", errors.New("GBCTL_TOKEN_FILE is empty")
		}
		return tok, nil
	}
	return "", errors.New("no admin token: set GBCTL_TOKEN or GBCTL_TOKEN_FILE")
}

// redact makes sure an error message (for example a URL echo) never carries
// the token.
func redact(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[redacted]")
}

func printHuman(w io.Writer, cmd string, resp *admin.Response) {
	if resp == nil {
		_, _ = fmt.Fprintln(w, "ok")
		return
	}
	switch cmd {
	case "update", "segment", "quiesce", "resume":
		_, _ = fmt.Fprintf(w, "%s: ok\n", cmd)
	case "universe":
		rec, ok := resp.Result.(*db.UniverseRecord)
		if !ok {
			_, _ = fmt.Fprintln(w, "universe init: reply not validated")
			return
		}
		_, _ = fmt.Fprintln(w, "universe init: ok")
		_, _ = fmt.Fprintf(w, "  seed %d, %d stars, %d planets (%d..%d per star), generator %s, %s by %s\n",
			rec.Seed, rec.Stars, rec.Planets, rec.MinPlanets, rec.MaxPlanets, rec.Generator, rec.InitializedAt.Format(time.RFC3339), rec.Tool)
	}
	if st := resp.Status; st != nil {
		_, _ = fmt.Fprintf(w, "mode:          %s\n", st.Mode)
		if st.HaltReason != "" {
			_, _ = fmt.Fprintf(w, "halt reason:   %s\n", st.HaltReason)
		}
		if st.Draining {
			_, _ = fmt.Fprintf(w, "draining:      true\n")
		}
		_, _ = fmt.Fprintf(w, "updates:       %d (next %s)\n", st.NumUpdates, st.NextUpdate.Local().Format(time.RFC3339))
		_, _ = fmt.Fprintf(w, "segments:      %d/%d (next %s)\n", st.NumSegments, st.MovesPerUpdate-1, st.NextSegment.Local().Format(time.RFC3339))
		_, _ = fmt.Fprintf(w, "world:         %d stars, %d races, %d ships\n", st.Stars, st.Races, st.Ships)
		if st.Universe != nil {
			_, _ = fmt.Fprintf(w, "universe:      seed %d, initialised %s by %s\n", st.Universe.Seed, st.Universe.InitializedAt.Format(time.RFC3339), st.Universe.Tool)
		}
		if st.TurnRunning {
			_, _ = fmt.Fprintf(w, "turn running:  since %s\n", st.TurnStarted.Format(time.RFC3339))
		}
		_, _ = fmt.Fprintf(w, "persistence:   %s\n", st.Persistence)
	}
}
