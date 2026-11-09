package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/phreddrick/GB/go/internal/admin"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/operatorenroll"
)

func enrollmentCommand(ctx context.Context, client *admin.Client, args []string, asJSON bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitUsage
	}
	if args[0] == "credentials" {
		return prepareCredentials(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "", "protected operator request JSON file")
	request := fs.String("request-id", "", "existing enrollment request ID (status only)")
	account := fs.String("account", "", "configured account binding (status only)")
	gameID := fs.String("game", "", "target game (status only)")
	god := fs.Bool("god", false, "explicitly request authorized first-God bootstrap")
	guest := fs.Bool("guest", false, "explicitly request authorized Guest creation")
	if fs.Parse(args[1:]) != nil || len(fs.Args()) != 0 {
		return exitUsage
	}
	var response *admin.Response
	var e error
	switch args[0] {
	case "enroll":
		if *file == "" || *request != "" || *account != "" || *gameID != "" {
			_, _ = fmt.Fprintln(stderr, "gbctl enroll requires -f operator-request.json; credentials are provisioned separately")
			return exitUsage
		}
		data, readErr := operatorenroll.ReadProtected(*file)
		var req enrollmentapi.OperatorRequest
		if readErr != nil || enrollmentapi.DecodeBody(strings.NewReader(string(data)), &req) != nil || !req.Query().Valid() {
			_, _ = fmt.Fprintln(stderr, "gbctl: invalid or unprotected enrollment request file")
			return exitUsage
		}
		if *god {
			req.God = true
		}
		if *guest {
			req.Guest = true
		}
		if req.God && req.Guest {
			return exitUsage
		}
		response, e = client.Enrollment(ctx, req)
	case "enroll-status":
		if *file != "" || *god || *guest {
			return exitUsage
		}
		q := enrollmentapi.Query{RequestID: *request, GameID: *gameID, AccountID: *account}
		if !q.Valid() {
			_, _ = fmt.Fprintln(stderr, "gbctl enroll-status requires -request-id, -game and -account")
			return exitUsage
		}
		response, e = client.EnrollmentStatus(ctx, q)
	default:
		return exitUsage
	}
	if e != nil {
		if errors.Is(e, admin.ErrUnauthorized) {
			_, _ = fmt.Fprintln(stderr, "gbctl: admin token rejected")
			return exitAuth
		}
		var refusal *admin.Error
		if errors.As(e, &refusal) {
			_, _ = fmt.Fprintf(stderr, "gbctl: enrollment response: %s\n", refusal.Msg)
		} else {
			_, _ = fmt.Fprintln(stderr, "gbctl: outcome unknown; request may still be executing; query enroll-status with the SAME request ID, game and account")
		}
		if response != nil {
			printEnrollment(stdout, response, asJSON)
		}
		if errors.As(e, &refusal) && refusal.Code >= 400 && refusal.Code < 500 {
			return exitRefused
		}
		return exitServer
	}
	printEnrollment(stdout, response, asJSON)
	if response == nil {
		return exitServer
	}
	view, ok := response.Result.(*enrollmentapi.View)
	if !ok {
		return exitServer
	}
	if view.State == enrollmentapi.Unknown || view.State == enrollmentapi.Reconciliation {
		return exitServer
	}
	return exitOK
}
func printEnrollment(w io.Writer, response *admin.Response, asJSON bool) {
	if response == nil {
		return
	}
	if asJSON {
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	view, ok := response.Result.(*enrollmentapi.View)
	if !ok {
		return
	}
	_, _ = fmt.Fprintf(w, "enrollment: %s; persistence: %s; replay: %t\n", view.State, view.Outcome, view.Replay)
	if p := view.Placement; p != nil {
		_, _ = fmt.Fprintf(w, "player %d; home %d/%d; capital (%d,%d); government ship %d\n", p.Player, p.Star, p.Planet, p.CapitalX, p.CapitalY, p.GovernmentShip)
	}
	if view.State != enrollmentapi.Confirmed {
		_, _ = fmt.Fprintln(w, "No confirmed success is asserted. Absence is not rollback evidence; retain the same request ID.")
	}
}
func prepareCredentials(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "prepare" {
		return exitUsage
	}
	fs := flag.NewFlagSet("credentials prepare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	account := fs.String("account", "", "configured account ID")
	raceFile := fs.String("race-file", "", "owner-only file containing race password")
	governorFile := fs.String("governor-file", "", "owner-only file containing governor password")
	output := fs.String("out", "", "new owner-only hashed account artifact")
	god := fs.Bool("god-bootstrap", false, "mark account as a bootstrap candidate; server policy must separately enable it")
	guest := fs.Bool("allow-guest", false, "request Guest permission in the provisioning artifact")
	if fs.Parse(args[1:]) != nil || len(fs.Args()) != 0 || *output == "" || *raceFile == "" || *governorFile == "" {
		return exitUsage
	}
	race, e := operatorenroll.ReadProtected(*raceFile)
	if e != nil {
		_, _ = fmt.Fprintln(stderr, "gbctl: protected race credential file unavailable")
		return exitUsage
	}
	governor, e := operatorenroll.ReadProtected(*governorFile)
	if e != nil {
		_, _ = fmt.Fprintln(stderr, "gbctl: protected governor credential file unavailable")
		return exitUsage
	}
	oneLine := func(b []byte) string { return strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r") }
	artifact, e := operatorenroll.Provision(*account, oneLine(race), oneLine(governor))
	if e != nil {
		_, _ = fmt.Fprintln(stderr, "gbctl: credentials require distinct UTF-8 passwords, 12..72 bytes, without embedded line endings or NUL")
		return exitUsage
	}
	artifact.GodBootstrap = *god
	artifact.AllowGuest = *guest
	if operatorenroll.WriteAccount(*output, artifact) != nil {
		_, _ = fmt.Fprintln(stderr, "gbctl: could not exclusively create protected credential artifact")
		return exitServer
	}
	_, _ = fmt.Fprintln(stdout, "Protected hashed account artifact created. Merge it into the server's protected authority file; no enrollment was performed.")
	return exitOK
}
