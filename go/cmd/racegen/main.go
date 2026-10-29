// racegen is the Galactic Bloodshed race creation / enrollment tool.
// It ports the interactive logic of source/racegen.c to Go.
//
// Usage:
//
//	racegen [-db path]
//
// The program runs an interactive REPL; type "help" for a list of commands.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// ── attribute indices (mirrors racegen.h) ─────────────────────────────────

const (
	attrAdvent = iota
	attrAbsorb
	attrBirth
	attrColIQ
	attrFert
	attrIQ
	attrFight
	attrPods
	attrMass
	attrSexes
	attrMetab
	numAttrs
)

// attrDef holds the cost-formula parameters for one racial attribute.
// Mirrors the `attribute` struct in racegen.h.
type attrDef struct {
	name       string
	eFactor    float64 // exponential factor
	eFudge     float64 // exponential scale
	eHinge     float64 // exponential offset point
	lFactor    float64 // linear factor
	lFudge     float64 // constant additive (computed at init to make default cost 0)
	minimum    float64
	initVal    float64
	maximum    float64
	isIntegral int // 0=float, 1=int, 2=bool
}

// attrs holds the attribute definitions.  Mirrors the attr[] table in
// racegen.c.  l_fudge is computed at runtime (see initFudges).
var attrs = [numAttrs]attrDef{
	attrAdvent: {"Adventurism", 0.0, 0.0, 0.0, 300.0, 0.0, 0.05, 0.4, 0.99, 0},
	attrAbsorb: {"Absorb", 0.0, 0.0, 0.0, 200.0, 0.0, 0.0, 0.0, 1.0, 2},
	attrBirth:  {"Birthrate", 0.0, 0.0, 0.0, 500.0, 0.0, 0.2, 0.6, 1.0, 0},
	attrColIQ:  {"Collective IQ", 0.0, 0.0, 0.0, -350.5, 0.0, 0.0, 0.0, 1.0, 2},
	attrFert:   {"Fertilize", 200.0, 1.0, 1.0, 300.0, 0.0, 0.0, 0.0, 1.0, 0},
	attrIQ:     {"IQ", 100.0, 0.03, 140.0, 6.0, 0.0, 50.0, 150.0, 220.0, 1},
	attrFight:  {"Fight", 10.0, 0.4, 6.0, 65.0, 0.0, 1.0, 4.0, 20.0, 1},
	attrPods:   {"Pods", 0.0, 0.0, 0.0, 200.0, 0.0, 0.0, 0.0, 1.0, 2},
	attrMass:   {"Mass", 100.0, 1.0, 3.1, -100.0, 0.0, 0.1, 1.0, 3.0, 0},
	attrSexes:  {"Sexes", 2.2, -0.5, 9.0, -3.0, 0.0, 1.0, 2.0, 53.0, 1},
	attrMetab:  {"Metabolism", 300.0, 1.0, 1.3, 700.0, 0.0, 0.1, 1.0, 4.0, 0},
}

// attrCovariance[i][j] — normal-race cross-attribute cost covariances.
// Mirrors the assignments in normal() in racegen.c.
var normalCov [numAttrs][numAttrs]float64

func initNormalCov() {
	attrRange := func(a int) float64 { return attrs[a].maximum - attrs[a].minimum }
	normalCov[attrAdvent][attrIQ] = -0.40 / attrRange(attrIQ)
	normalCov[attrBirth][attrIQ] = 0.20 / attrRange(attrIQ)
	normalCov[attrBirth][attrMass] = 0.40 / attrRange(attrMass)
	normalCov[attrBirth][attrSexes] = 0.90 / attrRange(attrSexes)
	normalCov[attrBirth][attrMetab] = -0.20 / attrRange(attrMetab)
	normalCov[attrFert][attrMetab] = -0.20 / attrRange(attrMetab)
	normalCov[attrFight][attrIQ] = -0.20 / attrRange(attrIQ)
	normalCov[attrFight][attrAdvent] = -0.05 / attrRange(attrAdvent)
	normalCov[attrFight][attrMass] = -0.20 / attrRange(attrMass)
	normalCov[attrFight][attrMetab] = -0.05 / attrRange(attrMetab)
	normalCov[attrMetab][attrMass] = 0.15 / attrRange(attrMass)
	normalCov[attrMetab][attrIQ] = -0.10 / attrRange(attrIQ)
	normalCov[attrIQ][attrMass] = -0.25 / attrRange(attrMass)
}

// ── sector compat covariances (mirrors compat_cov in racegen.c) ───────────

// compatCov[j][i]: must use i < j to avoid double-counting.
var compatCov = [8][8]float64{
	0: {},
	1: {0: 0.001},
	2: {0: 0.002, 1: -0.0005},
	3: {0: 999, 1: 999, 2: 999},
	4: {0: 0.001, 2: -0.002, 3: 999},
	5: {0: 0.0, 1: -0.001, 3: 999, 4: 0.001},
	6: {0: 0.003, 1: -0.0005, 3: 999, 5: 0.001},
	7: {3: 999},
}

// planet-type cross compat covariance multipliers (7 home types × 8 sector types)
var planetCompatCov = [7][8]float64{
	0: {1.00, 1.00, 2.00, 99.00, 1.01, 1.50, 3.00, 1.01},       // Earth
	1: {1.01, 1.50, 2.00, 99.00, 1.01, 1.00, 3.00, 1.01},       // Forest
	2: {3.00, 1.01, 1.01, 99.00, 1.50, 3.00, 1.00, 1.01},       // Desert
	3: {1.00, 1.50, 3.00, 99.00, 1.01, 1.01, 3.00, 1.01},       // Water
	4: {1.01, 1.00, 1.00, 99.00, 1.01, 1.01, 1.00, 1.01},       // Airless
	5: {3.00, 1.01, 1.00, 99.00, 1.00, 1.50, 2.00, 1.01},       // Iceball
	6: {99.00, 99.00, 99.00, 1.00, 99.00, 99.00, 99.00, 99.00}, // Jovian
}

// planet_cost per home type.
var planetCost = [7]int{75, 50, 50, 50, -25, -25, 600}

// blah: n_sector_types cost (based on number of non-zero compats).
var nSectorCost = [8]int{-1, 0, 50, 100, 200, 300, 400, 500}

const startingPoints = 1400

// ── race builder state ────────────────────────────────────────────────────

// raceState holds the mutable race-in-progress during racegen.
type raceState struct {
	name           string
	password       string
	address        string
	info           string // flavor text / description
	homePlanetType int    // 0=Earth 1=Forest 2=Desert 3=Water 4=Airless 5=Iceball 6=Jovian
	raceType       int    // 0=Normal 1=Metamorph
	attr           [numAttrs]float64
	compat         [8]float64 // sector compatibilities 0–100
}

func defaultRace() raceState {
	r := raceState{
		name:           "Unknown",
		password:       "XXXX",
		address:        "Unknown",
		homePlanetType: 0,
		raceType:       0,
	}
	for i := 0; i < numAttrs; i++ {
		r.attr[i] = attrs[i].initVal
	}
	r.compat[7] = 100 // plated always 100 for non-jovian
	return r
}

// initFudges sets attrs[i].lFudge so that the init attribute values cost 0.
// Mirrors the initialize() function in racegen.c.
func initFudges(r *raceState) {
	costs := attrCosts(r)
	for i := 0; i < numAttrs; i++ {
		attrs[i].lFudge += -costs[i]
	}
}

// ── cost calculation ──────────────────────────────────────────────────────

// attrRawCost returns the raw cost for attribute i at value v,
// before covariance adjustments, using the current l_fudge.
func attrRawCost(i int, v float64) float64 {
	a := attrs[i]
	return math.Exp(a.eFudge*(v-a.eHinge))*a.eFactor + v*a.lFactor
}

// attrCosts computes the cost of each attribute with covariance.
// Mirrors cost_of_race() in racegen.c (attribute portion only).
func attrCosts(r *raceState) [numAttrs]float64 {
	var c [numAttrs]float64
	for i := 0; i < numAttrs; i++ {
		c[i] = attrRawCost(i, r.attr[i])
	}
	// apply covariances
	for i := 0; i < numAttrs; i++ {
		for j := 0; j < numAttrs; j++ {
			if normalCov[i][j] != 0.0 {
				c[i] *= (1.0 + normalCov[i][j]*r.attr[j])
			}
		}
	}
	// add l_fudge
	for i := 0; i < numAttrs; i++ {
		c[i] = math.Round(c[i] + attrs[i].lFudge)
	}
	return c
}

// compatCosts computes sector compatibility costs.
// Mirrors cost_of_race() sector portion in racegen.c.
func compatCosts(r *raceState) [8]float64 {
	var c [8]float64
	for i := 0; i < 8; i++ {
		c[i] = r.compat[i]*0.5 + 10.8*math.Log(1.0+r.compat[i])
	}
	// cross-compat covariance
	for i := 0; i < 8; i++ {
		for j := i + 1; j < 8; j++ {
			if compatCov[j][i] != 0.0 {
				c[i] *= (1.0 + compatCov[j][i]*r.compat[j])
				c[j] *= (1.0 + compatCov[j][i]*r.compat[i])
			}
		}
	}
	// planet-compat cost multiplier
	pt := r.homePlanetType
	for i := 0; i < 8; i++ {
		if planetCompatCov[pt][i] > 1.01 {
			c[i] *= planetCompatCov[pt][i]
		}
	}
	for i := 0; i < 8; i++ {
		c[i] = math.Round(c[i])
	}
	return c
}

// nSectorTypes returns the count of non-zero sector compats.
func nSectorTypes(r *raceState) int {
	n := 0
	for i := 0; i < 8; i++ {
		if r.compat[i] != 0.0 {
			n++
		}
	}
	return n
}

// totalCost returns (costSpent, pointsLeft).
func totalCost(r *raceState) (int, int) {
	ac := attrCosts(r)
	cc := compatCosts(r)
	n := nSectorTypes(r)

	sum := 0
	for i := 0; i < numAttrs; i++ {
		sum += int(ac[i])
	}
	for i := 0; i < 8; i++ {
		sum += int(cc[i])
	}
	sum += planetCost[r.homePlanetType]
	if n < len(nSectorCost) {
		sum += nSectorCost[n]
	}
	return sum, startingPoints - sum
}

// ── validation ────────────────────────────────────────────────────────────

type valError struct{ msg string }

func validate(r *raceState, rigorous bool) []string {
	var errs []string
	add := func(s string) { errs = append(errs, s) }

	for i := 0; i < numAttrs; i++ {
		v := r.attr[i]
		a := attrs[i]
		if a.isIntegral == 2 && v != 0.0 && v != 1.0 {
			add(fmt.Sprintf("%s is boolean; use 0 or 1.", a.name))
		}
		if v < a.minimum {
			add(fmt.Sprintf("%s must be at least %.2f.", a.name, a.minimum))
		}
		if v > a.maximum {
			add(fmt.Sprintf("%s may be at most %.2f.", a.name, a.maximum))
		}
	}

	if r.name == "" {
		add("Use a non-empty name.")
	}

	// Normal races: no absorb, no pods
	if r.raceType == 0 {
		if r.attr[attrAbsorb] != 0.0 {
			add("Normal races do not absorb their enemies in combat.")
		}
		if r.attr[attrPods] != 0.0 {
			add("Normal races do not make pods.")
		}
	}

	// Sector compat validity
	if r.homePlanetType != 6 && r.compat[7] != 100.0 {
		add("Non-jovian races must have 100% plated compat.")
	}
	for i := 1; i < 8; i++ {
		if r.compat[i] < 0.0 {
			add("Sector compatibility is at minimum 0%.")
		}
		if r.compat[i] > 100.0 {
			add("Sector compatibility may be at most 100%.")
		}
		if i == 3 && r.compat[i] != 0.0 && r.homePlanetType != 6 {
			add("Non-jovian races may never have gas compatibility!")
		}
		if i != 3 && r.compat[i] != 0.0 && r.homePlanetType == 6 {
			add("Jovian races may have no compatibility other than gas!")
		}
	}

	if rigorous {
		if len(r.password) < 4 {
			add("Passwords are required to be at least 4 characters long.")
		}
		if r.password == "XXXX" {
			add("You must change your password from the default.")
		}
		_, pts := totalCost(r)
		if pts < 0 {
			add("You can't have negative points left!")
		}
		n := nSectorTypes(r)
		if r.homePlanetType != 6 && n <= 1 {
			add("Non-jovian races must be compat with at least one sector type besides plated.")
		}
	}

	return errs
}

// ── output helpers ────────────────────────────────────────────────────────

var homePlanetNames = []string{"Earth", "Forest", "Desert", "Water", "Airless", "Iceball", "Jovian"}
var raceTypeNames = []string{"Normal", "Metamorph"}
var sectorNames = []string{"Water", "Land", "Mountain", "Gas", "Ice", "Forest", "Desert", "Plated"}

func printRace(r *raceState) {
	spent, pts := totalCost(r)
	fmt.Printf("\n──────────────────────────────────────────\n")
	fmt.Printf("Race: %-20s  Points: %d left (%d spent / %d total)\n",
		r.name, pts, spent, startingPoints)
	fmt.Printf("Address: %s\n", r.address)
	fmt.Printf("Home planet: %-10s  Race type: %s\n",
		homePlanetNames[r.homePlanetType], raceTypeNames[r.raceType])
	fmt.Printf("\nAttributes:\n")
	ac := attrCosts(r)
	for i := 0; i < numAttrs; i++ {
		a := attrs[i]
		fmt.Printf("  %-16s %8.2f  (cost %4d)  [%.2f – %.2f]\n",
			a.name, r.attr[i], int(ac[i]), a.minimum, a.maximum)
	}
	fmt.Printf("\nSector Compatibilities:\n")
	cc := compatCosts(r)
	for i := 0; i < 8; i++ {
		fmt.Printf("  %-10s %6.0f%%  (cost %4d)\n",
			sectorNames[i], r.compat[i], int(cc[i]))
	}
	n := nSectorTypes(r)
	if n < len(nSectorCost) {
		fmt.Printf("  N-sector-types cost: %d (%d types)\n", nSectorCost[n], n)
	}
	fmt.Printf("  Planet type cost: %d\n", planetCost[r.homePlanetType])
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
  enroll                   Validate and save race to database
  quit                     Exit without saving

Fields for modify:
  name <str>               Race name
  password <str>           Password (min 4 chars)
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
	return v, err == nil
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
		r.password = val
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

// buildModelRace converts raceState into a model.Race ready for storage.
// The player number is allocated from the DB.
func buildModelRace(r *raceState, playerNum int) *model.Race {
	hash, err := bcrypt.GenerateFromPassword([]byte(r.password), bcrypt.DefaultCost)
	if err != nil {
		// Fallback: store plain-text if bcrypt fails (should never happen).
		hash = []byte(r.password)
	}
	hashedPassword := string(hash)

	race := &model.Race{
		PlayerNum:    playerNum,
		Name:         r.name,
		Password:     hashedPassword,
		Info:         r.info,
		Adventurism:  r.attr[attrAdvent],
		BirthRate:    r.attr[attrBirth],
		CollectiveIQ: r.attr[attrColIQ] >= 1.0,
		Absorb:       r.attr[attrAbsorb] >= 1.0,
		Pods:         r.attr[attrPods] >= 1.0,
		Fertilize:    uint8(r.attr[attrFert]),
		IQ:           uint8(r.attr[attrIQ]),
		IQLimit:      uint8(r.attr[attrIQ]),
		Fighters:     uint8(r.attr[attrFight]),
		Mass:         r.attr[attrMass],
		NumberSexes:  uint8(r.attr[attrSexes]),
		Metabolism:   r.attr[attrMetab],
		LikesBest:    uint8(r.homePlanetType),
		Metamorph:    r.raceType == 1,
	}
	// Likes: sector compat as 0–1 fractions
	for i := 0; i < 8 && i < len(race.Likes); i++ {
		race.Likes[i] = r.compat[i] / 100.0
	}
	// Map home planet type to GB planet-type constant
	// C: H_EARTH=0 H_FOREST=1 H_DESERT=2 H_WATER=3 H_AIRLESS=4 H_ICEBALL=5 H_JOVIAN=6
	// model: Earth=0 Forest=6 Desert=7 Water=5 Asteroid=1 Iceball=3 GasGiant=4
	homePlanetMap := [7]uint8{
		model.PlanetEarth,
		model.PlanetForest,
		model.PlanetDesert,
		model.PlanetWater,
		model.PlanetAsteroid,
		model.PlanetIceball,
		model.PlanetGasGiant,
	}
	race.LikesBest = homePlanetMap[r.homePlanetType]

	// Set governor 0 as active
	race.Governors = 1
	race.Governor[0] = model.Governor{
		Name:     r.name,
		Password: hashedPassword,
		Active:   true,
	}

	return race
}

// ── main ─────────────────────────────────────────────────────────────────

var flagDB = flag.String("db", "./gb.db", "path to bbolt database file")

func main() {
	flag.Parse()

	// Set up covariance tables and fudge factors
	initNormalCov()
	def := defaultRace()
	// Calculate fudges with init values
	initFudges(&def)

	r := defaultRace()

	fmt.Printf("Galactic Bloodshed Race Generator\n")
	fmt.Printf("Starting points: %d\n", startingPoints)
	fmt.Printf("Type 'help' for a list of commands.\n\n")
	printRace(&r)

	for {
		line := prompt("racegen> ")
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "help", "h", "?":
			doHelp()

		case "print", "p":
			printRace(&r)

		case "modify", "mod", "m":
			if len(parts) < 3 {
				fmt.Println("Usage: modify <field> <value>")
			} else {
				doModify(&r, parts[1:])
			}

		case "validate", "check":
			errs := validate(&r, true)
			if len(errs) == 0 {
				fmt.Println("Race is valid.")
			} else {
				for _, e := range errs {
					fmt.Println(" -", e)
				}
			}

		case "enroll", "save":
			errs := validate(&r, true)
			if len(errs) > 0 {
				fmt.Printf("Race (%s) unacceptable:\n", r.name)
				for _, e := range errs {
					fmt.Println(" -", e)
				}
				fmt.Println("Fix the errors above and try again.")
				continue
			}

			store, err := db.Open(*flagDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
				continue
			}

			// Allocate player number = next available slot
			races, err := store.AllRaces()
			if err != nil {
				store.Close()
				fmt.Fprintf(os.Stderr, "Error reading races: %v\n", err)
				continue
			}
			playerNum := 1
			for playerNum <= model.MaxPlayers && races[playerNum-1] != nil {
				playerNum++
			}
			if playerNum > model.MaxPlayers {
				store.Close()
				fmt.Fprintf(os.Stderr, "No free player slots.\n")
				continue
			}

			race := buildModelRace(&r, playerNum)
			if err := store.PutRace(race); err != nil {
				store.Close()
				fmt.Fprintf(os.Stderr, "Error writing race: %v\n", err)
				continue
			}
			store.Close()
			fmt.Printf("Race %q enrolled as player %d.\n", r.name, playerNum)

		case "quit", "exit", "q":
			errs := validate(&r, false)
			if len(errs) > 0 {
				ans := prompt("Race has issues. Quit anyway? [y/n] ")
				if !strings.HasPrefix(strings.ToLower(ans), "y") {
					continue
				}
			} else if r.name != "Unknown" {
				ans := prompt("Quit without enrolling? [y/n] ")
				if !strings.HasPrefix(strings.ToLower(ans), "y") {
					continue
				}
			}
			fmt.Println("Goodbye.")
			os.Exit(0)

		default:
			fmt.Printf("Unknown command %q. Type 'help'.\n", cmd)
		}
	}
}
