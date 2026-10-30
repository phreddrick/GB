// makeuniv generates a new Galactic Bloodshed universe and writes it to a
// bbolt database that no server is using. Since Phase 1 the generation code
// lives in internal/universe (the same code the running server uses for
// `gbctl universe init`); this command is the offline wrapper for it.
//
// It refuses to touch a database that already holds a universe (CR-05). To
// recreate a disposable development universe, stop the server and delete the
// database file first; there is no overwrite flag.
//
// Exit status: 0 universe written (or, with -skip-if-initialized, nothing to
// do); 1 usage or generation error; 2 database error; 3 database already
// initialised.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/universe"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("makeuniv", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stars := fs.Int("s", universe.DefaultStars, fmt.Sprintf("number of stars (1-%d)", model.NumStars-1))
	minPlanets := fs.Int("l", universe.DefaultMinPlanets, fmt.Sprintf("minimum planets per system (1-%d)", model.MaxPlanets))
	maxPlanets := fs.Int("m", universe.DefaultMaxPlanets, fmt.Sprintf("maximum planets per system (min-%d)", model.MaxPlanets))
	seed := fs.Int64("seed", 0, "generation seed (0 = choose a random seed and print it)")
	starList := fs.String("starlist", "", "optional file with one star name per line")
	planetList := fs.String("planlist", "", "optional file with one planet name per line")
	dbPath := fs.String("db", "./gb.db", "path to the bbolt database file (must not be open by a server)")
	skipIfInit := fs.Bool("skip-if-initialized", false, "exit 0 without writing when the database already holds a universe (for idempotent bootstrap scripts)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	params := universe.Params{Stars: *stars, MinPlanets: *minPlanets, MaxPlanets: *maxPlanets}
	if err := params.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "makeuniv: %v\n", err)
		return 1
	}
	var err error
	if params.StarNames, err = readNameList(*starList); err != nil {
		_, _ = fmt.Fprintf(stderr, "makeuniv: star list: %v\n", err)
		return 1
	}
	if params.PlanetNames, err = readNameList(*planetList); err != nil {
		_, _ = fmt.Fprintf(stderr, "makeuniv: planet list: %v\n", err)
		return 1
	}
	if *seed == 0 {
		*seed = randomSeed()
	}

	store, err := db.Open(*dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "makeuniv: open database %q: %v (is a server running on it?)\n", *dbPath, err)
		return 2
	}
	defer func() { _ = store.Close() }()

	rec, err := universe.Init(store, params, *seed, "makeuniv", time.Now())
	switch {
	case errors.Is(err, universe.ErrAlreadyInitialized):
		if *skipIfInit {
			_, _ = fmt.Fprintf(stdout, "makeuniv: %s already holds a universe; nothing done\n", *dbPath)
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "makeuniv: refusing: %s already holds a universe (CR-05). Stop the server and delete the file to recreate a disposable universe, or pass -skip-if-initialized for idempotent bootstrap.\n", *dbPath)
		return 3
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "makeuniv: %v\n", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "Universe generation complete: %d stars, %d planets written to %s (seed %d, %s)\n",
		rec.Stars, rec.Planets, *dbPath, rec.Seed, rec.InitializedAt.Format(time.RFC3339))
	return 0
}

// readNameList reads non-empty lines from filename; an empty filename yields nil.
func readNameList(filename string) ([]string, error) {
	if filename == "" {
		return nil, nil
	}
	f, err := os.Open(filename) //nolint:gosec // G304: the operator names the list file on purpose
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			names = append(names, line)
		}
	}
	return names, sc.Err()
}

// randomSeed draws a non-zero seed from the operating system.
func randomSeed() int64 {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return time.Now().UnixNano()
		}
		//nolint:gosec // G115: masking to 63 bits keeps the seed positive; any value is a valid seed
		if s := int64(binary.LittleEndian.Uint64(b[:]) & (1<<63 - 1)); s != 0 {
			return s
		}
	}
}
