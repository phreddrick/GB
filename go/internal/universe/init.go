package universe

import (
	"errors"
	"fmt"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// GeneratorVersion is recorded with every initialisation so that a later
// change to the algorithm is visible in the metadata.
const GeneratorVersion = "makeuniv-go/1"

// ErrAlreadyInitialized is returned when the store already holds a universe.
var ErrAlreadyInitialized = db.ErrAlreadyInitialized

// Init generates a universe from params and seed and writes it, with its
// record, to store in one transaction. It refuses when the store is already
// initialised (there is deliberately no overwrite option: destroying an
// existing universe is an offline operation on disposable data, documented
// in docs/operations/local-server.md). Nothing is written on failure.
func Init(store *db.Store, params Params, seed int64, tool string, now time.Time) (*db.UniverseRecord, error) {
	params = params.withDefaults()
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("universe init: %w", err)
	}
	initialized, err := store.IsInitialized()
	if err != nil {
		return nil, fmt.Errorf("universe init: check store: %w", err)
	}
	if initialized {
		return nil, ErrAlreadyInitialized
	}
	u, err := Generate(params, seed)
	if err != nil {
		return nil, fmt.Errorf("universe init: %w", err)
	}
	rec := &db.UniverseRecord{
		Seed:          seed,
		Stars:         params.Stars,
		MinPlanets:    params.MinPlanets,
		MaxPlanets:    params.MaxPlanets,
		Planets:       len(u.Planets),
		Generator:     GeneratorVersion,
		InitializedAt: now.UTC(),
		Tool:          tool,
	}
	if err := store.PutUniverse(u.StarData, u.Stars, u.Planets, rec); err != nil {
		if errors.Is(err, db.ErrAlreadyInitialized) {
			return nil, ErrAlreadyInitialized
		}
		return nil, fmt.Errorf("universe init: write: %w", err)
	}
	return rec, nil
}
