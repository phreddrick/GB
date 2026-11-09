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
	password       string
	address        string
	info           string // flavor text / description
	homePlanetType int    // 0=Earth 1=Forest 2=Desert 3=Water 4=Airless 5=Iceball 6=Jovian
	raceType       int    // 0=Normal 1=Metamorph
	attr           [numAttrs]float64
	compat         [8]float64 // sector compatibilities 0–100
}

func defaultRace() raceState {
	s := racegen.Default()
	return raceState{name: s.Name, password: "XXXX", address: "Unknown", attr: s.Attributes.Values(), compat: s.SectorCompat}
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
	if rigorous {
		if len(r.password) < 4 {
			errs = append(errs, "Passwords are required to be at least 4 characters long.")
		}
		if r.password == "XXXX" {
			errs = append(errs, "You must change your password from the default.")
		}
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
