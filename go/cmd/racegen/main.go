// racegen is an offline Galactic Bloodshed species designer.
// It ports the interactive logic of source/racegen.c to Go.
//
// Usage:
//
//	racegen [-load draft.json] [-save new-draft.json]
//
// The program runs an interactive REPL; type "help" for a list of commands.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/racegen"
)

// CLI field aliases retain the existing command vocabulary. Rules live in racegen.
const (
	attrAdvent     = racegen.Adventurism
	attrAbsorb     = racegen.Absorb
	attrBirth      = racegen.Birthrate
	attrColIQ      = racegen.CollectiveIQ
	attrFert       = racegen.Fertilize
	attrIQ         = racegen.IQ
	attrFight      = racegen.Fighters
	attrPods       = racegen.Pods
	attrMass       = racegen.Mass
	attrSexes      = racegen.Sexes
	attrMetab      = racegen.Metabolism
	numAttrs       = racegen.AttributeCount
	startingPoints = racegen.Budget
)

// ── race builder state ────────────────────────────────────────────────────

// raceState holds the mutable race-in-progress during racegen.
type raceState struct {
	name           string
	address        string
	info           string // flavor text / description
	homePlanetType int    // 0=Earth 1=Forest 2=Desert 3=Water 4=Airless 5=Iceball 6=Jovian
	raceType       int    // 0=Normal 1=Metamorph
	attr           [numAttrs]float64
	compat         [8]float64 // sector compatibilities 0–100
}

func defaultRace() raceState {
	s := racegen.Default()
	return raceState{name: s.Name, address: "Unknown", attr: s.Attributes.Values(), compat: s.SectorCompat}
}

func (r *raceState) spec() racegen.Spec {
	return racegen.Spec{Name: r.name, Address: r.address, Info: r.info,
		Homeworld: racegen.Homeworld(r.homePlanetType), RaceType: racegen.RaceType(r.raceType),
		Attributes: racegen.AttributesFromValues(r.attr), SectorCompat: r.compat}
}

// Credential checks remain CLI-local; the shared design rules own no passwords.
func validate(r *raceState, rigorous bool) []string {
	var errs []string
	for _, e := range racegen.Validate(r.spec()) {
		if !rigorous && (e.Code == "OVER_BUDGET" || e.Code == "TERRAIN_COUNT" || e.Code == "COMMON_TERRAIN") {
			continue
		}
		errs = append(errs, e.Field+": "+e.Message)
	}

	return errs
}

// ── output helpers ────────────────────────────────────────────────────────

var homePlanetNames = func() []string {
	var names []string
	for _, h := range racegen.Homeworlds() {
		names = append(names, h.Name)
	}
	return names
}()
var raceTypeNames = []string{"Normal", "Metamorph"}
var sectorNames = func() []string {
	var names []string
	for _, s := range racegen.Sectors() {
		names = append(names, s.Name)
	}
	return names
}()

func printRace(r *raceState) {
	b, errs := racegen.Cost(r.spec())
	if len(errs) > 0 {
		fmt.Printf("Cannot calculate design cost: %s\n", errs.Error())
		return
	}
	fmt.Printf("\n──────────────────────────────────────────\n")
	fmt.Printf("Race: %-20s  Points: %d left (%d spent / %d total)\n", r.name, b.Remaining, b.Total, startingPoints)
	fmt.Printf("Address: %s\n", r.address)
	fmt.Printf("Home planet: %-10s  Race type: %s\n", homePlanetNames[r.homePlanetType], raceTypeNames[r.raceType])
	fmt.Printf("\nAttributes:\n")
	for i, a := range racegen.AttributeDefinitions() {
		fmt.Printf("  %-16s %8.2f  (cost %4d)  [%.2f – %.2f]\n", a.Name, r.attr[i], b.Attributes[i], a.Minimum, a.Maximum)
	}
	fmt.Printf("\nSector Compatibilities:\n")
	for i := range r.compat {
		fmt.Printf("  %-10s %6.0f%%  (cost %4d)\n", sectorNames[i], r.compat[i], b.Sectors[i])
	}
	fmt.Printf("  N-sector-types cost: %d (%d types)\n", b.SectorSurcharge, b.SectorCount)
	fmt.Printf("  Planet type cost: %d\n", b.Homeworld)
	fmt.Printf("──────────────────────────────────────────\n\n")
}

// ── interactive REPL ──────────────────────────────────────────────────────

var scanner = bufio.NewScanner(os.Stdin)

func prompt(msg string) string {
	fmt.Print(msg)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

func doHelp() {
	fmt.Print(`
Commands:
  print                    Print current race stats and points
  modify <field> <value>   Change a race attribute
  save <file>              Save complete racegen.Spec (no credentials)
  load <file>              Load a strict racegen.Spec JSON file
  enroll                   Retired; use authenticated gbctl enroll
  quit                     Exit without saving

Fields for modify:
  name <str>               Race name
  address <email>          Email address
  info <str>               Race description
  planet <type>            Home planet type (Earth Forest Desert Water Airless Iceball Jovian)
  race <type>              Race type (Normal Metamorph)
  adventurism <v>          Adventurism (0.05 – 0.99)
  absorb <0|1>             Absorb (metamorph only)
  birthrate <v>            Birth rate (0.2 – 1.0)
  collective_iq <0|1>      Collective IQ (bool)
  fertilize <v>            Fertilize (0.0 – 1.0)
  iq <v>                   IQ (50 – 220)
  fight <v>                Fight / fighters (1 – 20)
  pods <0|1>               Pods (metamorph only)
  mass <v>                 Mass (0.1 – 3.0)
  sexes <v>                Number of sexes (1 – 53)
  metabolism <v>           Metabolism (0.1 – 4.0)
  water <v>                Water sector compat (0 – 100)
  land <v>                 Land sector compat (0 – 100)
  mountain <v>             Mountain sector compat (0 – 100)
  gas <v>                  Gas sector compat (0 – 100, jovian only)
  ice <v>                  Ice sector compat (0 – 100)
  forest <v>               Forest sector compat (0 – 100)
  desert <v>               Desert sector compat (0 – 100)
  plated <v>               Plated sector compat (must be 100 for non-jovian)

`)
}

// parseFloat parses a float, returning ok=false on error.
func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func doModify(r *raceState, args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: modify <field> <value>")
		return
	}
	field := strings.ToLower(args[0])
	val := strings.Join(args[1:], " ")

	switch field {
	case "name":
		r.name = val
	case "password":
		fmt.Println("Credentials are not accepted by the offline designer; use gbctl credentials prepare.")
		return
	case "address":
		r.address = val
	case "info":
		r.info = val
	case "planet":
		matched := false
		for i, n := range homePlanetNames {
			if strings.EqualFold(n, val) {
				r.homePlanetType = i
				// Enforce jovian compat rules
				if i == 6 {
					for j := 0; j < 8; j++ {
						r.compat[j] = 0
					}
					r.compat[3] = 100 // gas
				} else {
					r.compat[7] = 100 // plated
					r.compat[3] = 0   // no gas
				}
				matched = true
				break
			}
		}
		if !matched {
			fmt.Printf("Unknown planet type %q. Options: %s\n", val, strings.Join(homePlanetNames, " "))
		}
	case "race":
		switch strings.ToLower(val) {
		case "normal":
			r.raceType = 0
			r.attr[attrAbsorb] = 0
			r.attr[attrPods] = 0
		case "metamorph":
			r.raceType = 1
		default:
			fmt.Printf("Unknown race type %q. Options: Normal Metamorph\n", val)
		}
	case "adventurism":
		if v, ok := parseFloat(val); ok {
			r.attr[attrAdvent] = v
		}
	case "absorb":
		if v, ok := parseFloat(val); ok {
			r.attr[attrAbsorb] = v
		}
	case "birthrate":
		if v, ok := parseFloat(val); ok {
			r.attr[attrBirth] = v
		}
	case "collective_iq", "collectiveiq":
		if v, ok := parseFloat(val); ok {
			r.attr[attrColIQ] = v
		}
	case "fertilize":
		if v, ok := parseFloat(val); ok {
			r.attr[attrFert] = v
		}
	case "iq":
		if v, ok := parseFloat(val); ok {
			r.attr[attrIQ] = v
		}
	case "fight", "fighters":
		if v, ok := parseFloat(val); ok {
			r.attr[attrFight] = v
		}
	case "pods":
		if v, ok := parseFloat(val); ok {
			r.attr[attrPods] = v
		}
	case "mass":
		if v, ok := parseFloat(val); ok {
			r.attr[attrMass] = v
		}
	case "sexes":
		if v, ok := parseFloat(val); ok {
			r.attr[attrSexes] = v
		}
	case "metabolism":
		if v, ok := parseFloat(val); ok {
			r.attr[attrMetab] = v
		}
	case "water":
		if v, ok := parseFloat(val); ok {
			r.compat[0] = v
		}
	case "land":
		if v, ok := parseFloat(val); ok {
			r.compat[1] = v
		}
	case "mountain":
		if v, ok := parseFloat(val); ok {
			r.compat[2] = v
		}
	case "gas":
		if v, ok := parseFloat(val); ok {
			r.compat[3] = v
		}
	case "ice":
		if v, ok := parseFloat(val); ok {
			r.compat[4] = v
		}
	case "forest":
		if v, ok := parseFloat(val); ok {
			r.compat[5] = v
		}
	case "desert":
		if v, ok := parseFloat(val); ok {
			r.compat[6] = v
		}
	case "plated":
		if v, ok := parseFloat(val); ok {
			r.compat[7] = v
		}
	default:
		fmt.Printf("Unknown field %q. Type 'help' for a list.\n", field)
		return
	}
	printRace(r)
}

// main is an offline designer only. Legacy -db is an explicit error and no DB
// package is imported. Submission belongs exclusively to authenticated gbctl.
func main() { os.Exit(runDesigner(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func runDesigner(args []string, input io.Reader, output, stderr io.Writer) int {
	fs := flag.NewFlagSet("racegen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	legacy := fs.String("db", "", "retired: direct database writes are forbidden")
	load := fs.String("load", "", "load a complete racegen.Spec JSON draft")
	save := fs.String("save", "", "save the complete draft as JSON and exit")
	if fs.Parse(args) != nil {
		return 1
	}
	usedLegacy := false
	fs.Visit(func(f *flag.Flag) { usedLegacy = usedLegacy || f.Name == "db" })
	if usedLegacy || *legacy != "" {
		_, _ = fmt.Fprintln(stderr, "racegen: -db is retired; use gbctl enroll through the private admin listener")
		return 1
	}
	if len(fs.Args()) != 0 {
		return 1
	}
	r := defaultRace()
	if *load != "" {
		if e := loadDraft(*load, &r); e != nil {
			_, _ = fmt.Fprintln(stderr, "racegen: invalid draft file")
			return 1
		}
	}
	if *save != "" {
		if e := saveDraft(*save, r.spec()); e != nil {
			_, _ = fmt.Fprintln(stderr, "racegen: could not create draft file")
			return 1
		}
		return 0
	}
	scan := bufio.NewScanner(input)
	_, _ = fmt.Fprintln(output, "Galactic Bloodshed offline species designer; no database access. Use gbctl enroll to submit.")
	for scan.Scan() {
		parts := strings.Fields(scan.Text())
		if len(parts) == 0 {
			continue
		}
		switch strings.ToLower(parts[0]) {
		case "quit", "exit", "q":
			return 0
		case "help", "h", "?":
			doHelp()
		case "print", "p":
			printRace(&r)
		case "modify", "mod", "m":
			doModify(&r, parts[1:])
		case "validate", "check":
			if errs := validate(&r, true); len(errs) > 0 {
				for _, e := range errs {
					_, _ = fmt.Fprintln(output, e)
				}
			} else {
				_, _ = fmt.Fprintln(output, "Design valid.")
			}
		case "save":
			if len(parts) != 2 || saveDraft(parts[1], r.spec()) != nil {
				_, _ = fmt.Fprintln(stderr, "racegen: save requires a new draft file path")
			}
		case "load":
			if len(parts) != 2 || loadDraft(parts[1], &r) != nil {
				_, _ = fmt.Fprintln(stderr, "racegen: load requires a valid draft file")
			}
		case "enroll":
			_, _ = fmt.Fprintln(stderr, "racegen: direct enrollment is retired; save the draft and use gbctl enroll")
		default:
			_, _ = fmt.Fprintln(stderr, "racegen: unknown command")
		}
	}
	if scan.Err() != nil {
		return 1
	}
	return 0
}
func saveDraft(path string, spec racegen.Spec) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // operator-selected draft, exclusive owner-only creation
	if e != nil {
		return e
	}
	encodeErr := json.NewEncoder(f).Encode(spec)
	closeErr := f.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
func loadDraft(path string, r *raceState) error {
	f, e := os.Open(path) //nolint:gosec // operator-selected offline draft
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	var spec racegen.Spec
	if e = enrollmentapi.DecodeBody(f, &spec); e != nil {
		return e
	}
	*r = raceState{name: spec.Name, address: spec.Address, info: spec.Info, homePlanetType: int(spec.Homeworld), raceType: int(spec.RaceType), attr: spec.Attributes.Values(), compat: spec.SectorCompat}
	return nil
}
