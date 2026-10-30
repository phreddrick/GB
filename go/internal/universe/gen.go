// Package universe generates a Galactic Bloodshed universe and initialises a
// store with it. The generation code is the former cmd/makeuniv logic
// (mirroring source/makeuniv.c, makestar.c and makeplanet.c) moved into a
// library so that the authoritative server process can run it through an
// administrative operation (CR-05) with a deterministic seed; the algorithm
// itself is unchanged. All randomness comes from one seeded generator, so the
// same Params and Seed always produce the same universe.
package universe

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/phreddrick/GB/go/internal/model"
)

// Params control generation. Zero values are filled by Params.withDefaults.
type Params struct {
	Stars      int // 1..model.NumStars-1
	MinPlanets int // 1..model.MaxPlanets
	MaxPlanets int // MinPlanets..model.MaxPlanets
	// StarNames and PlanetNames are optional name lists; when shorter than
	// needed the generator falls back to "Star-n" and planet ordinals, as
	// the non-interactive makeuniv did.
	StarNames   []string
	PlanetNames []string
	// RatioXY is the planet map aspect ratio (config RatioXY, 3.70 by default).
	RatioXY float64
}

// Default generation parameters (the former non-interactive makeuniv defaults).
const (
	DefaultStars      = 100
	DefaultMinPlanets = 2
	DefaultMaxPlanets = model.MaxPlanets
	defaultRatioXY    = 3.70
)

func (p Params) withDefaults() Params {
	if p.Stars == 0 {
		p.Stars = DefaultStars
	}
	if p.MinPlanets == 0 {
		p.MinPlanets = DefaultMinPlanets
	}
	if p.MaxPlanets == 0 {
		p.MaxPlanets = DefaultMaxPlanets
	}
	if p.RatioXY == 0 {
		p.RatioXY = defaultRatioXY
	}
	return p
}

// Validate reports the first out-of-range parameter.
func (p Params) Validate() error {
	p = p.withDefaults()
	switch {
	case p.Stars < 1 || p.Stars >= model.NumStars:
		return fmt.Errorf("stars must be 1..%d (got %d)", model.NumStars-1, p.Stars)
	case p.MinPlanets < 1 || p.MinPlanets > model.MaxPlanets:
		return fmt.Errorf("min planets must be 1..%d (got %d)", model.MaxPlanets, p.MinPlanets)
	case p.MaxPlanets < p.MinPlanets || p.MaxPlanets > model.MaxPlanets:
		return fmt.Errorf("max planets must be %d..%d (got %d)", p.MinPlanets, model.MaxPlanets, p.MaxPlanets)
	case p.RatioXY <= 0:
		return fmt.Errorf("ratio_xy must be positive")
	}
	return nil
}

// Universe is a generated world ready to be stored.
type Universe struct {
	StarData *model.StarData
	Stars    []*model.Star
	Planets  []*model.Planet // all planets, StarNum/PlanetNum set
}

// Generate builds a universe from params and seed. It never touches a store.
func Generate(params Params, seed int64) (*Universe, error) {
	params = params.withDefaults()
	if err := params.Validate(); err != nil {
		return nil, err
	}
	g := newGenerator(params, seed)
	u := &Universe{StarData: &model.StarData{NumStars: u16(params.Stars)}}
	for i := 0; i < params.Stars; i++ {
		star, planets := g.makeStar(u8(i), params.MinPlanets, params.MaxPlanets)
		u.Stars = append(u.Stars, star)
		for j := range planets {
			u.Planets = append(u.Planets, &planets[j])
		}
	}
	return u, nil
}

// generator holds the per-run state that cmd/makeuniv kept in globals.
type generator struct {
	rng         *rand.Rand
	ratioXY     float64
	starNames   []string
	planetNames []string
	starOrder   []int
	planetOrder []int
	starIdx     int
	planetIdx   int
	occupied    [100][100]bool
}

func newGenerator(p Params, seed int64) *generator {
	g := &generator{
		rng:         rand.New(rand.NewSource(seed)), //nolint:gosec // G404: deterministic world generation; not a security boundary
		ratioXY:     p.RatioXY,
		starNames:   p.StarNames,
		planetNames: p.PlanetNames,
	}
	if len(g.starNames) > 0 {
		g.starOrder = g.randList(len(g.starNames) - 1)
	}
	if len(g.planetNames) > 0 {
		g.planetOrder = g.randList(len(g.planetNames) - 1)
	}
	return g
}

// randList produces a randomised permutation of [0, n].
// Mirrors rand_list() in makestar.c.
func (g *generator) randList(n int) []int {
	nums := make([]int, n+1)
	list := make([]int, n+1)
	for j := 0; j <= n; j++ {
		i := g.intRand(0, n)
		k := i
		for nums[k] != 0 {
			k += nums[k]
		}
		list[j] = k
		kk := 0
		if k == n {
			nums[k] = -n
		} else {
			nums[k] = 1
			kk = k + 1
		}
		for i != k {
			ii := i + nums[i]
			nums[i] = kk - i
			i = ii
		}
	}
	return list
}

func (g *generator) nextStarName() string {
	if g.starIdx < len(g.starNames) {
		name := g.starNames[g.starOrder[g.starIdx]]
		g.starIdx++
		return name
	}
	name := fmt.Sprintf("Star-%d", g.starIdx+1)
	g.starIdx++
	return name
}

func (g *generator) nextPlanetName(i int) string {
	if g.planetIdx < len(g.planetNames) {
		name := g.planetNames[g.planetOrder[g.planetIdx]]
		g.planetIdx++
		return name
	}
	return fmt.Sprintf("%d", i+1)
}

// ── random helpers (mirror rand.c; seeded, per generator) ─────────────────

func (g *generator) intRand(low, hi int) int {
	if hi <= low {
		return low
	}
	return low + g.rng.Intn(hi-low+1)
}

func (g *generator) doubleRand() float64 { return g.rng.Float64() }

func (g *generator) roundRand(x float64) int {
	if x < 0 {
		return int(x)
	}
	fl := int(x)
	if g.doubleRand() > x-float64(fl) {
		return fl
	}
	return fl + 1
}

// Bounded integer conversions. Every value converted here is produced by the
// generator's own ranges (atmosphere percentages, map sizes, star numbers);
// the clamps make the bound explicit instead of relying on it silently.
func i16(v int) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

func u8(v int) uint8 {
	if v > math.MaxUint8 {
		return math.MaxUint8
	}
	if v < 0 {
		return 0
	}
	return uint8(v)
}

func u16(v int) uint16 {
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	if v < 0 {
		return 0
	}
	return uint16(v)
}

// ── star placement ────────────────────────────────────────────────────────

const univSize = 150000.0

// placeStar picks a random position for a star, avoiding already-occupied cells.
func (g *generator) placeStar(star *model.Star) {
	for {
		star.XPos = float64(g.intRand(int(-univSize), int(univSize)))
		star.YPos = float64(g.intRand(int(-univSize), int(univSize)))
		i := int(100 * (star.XPos + univSize) / (2 * univSize))
		j := int(100 * (star.YPos + univSize) / (2 * univSize))
		if i < 0 {
			i = 0
		}
		if i >= 100 {
			i = 99
		}
		if j < 0 {
			j = 0
		}
		if j >= 100 {
			j = 99
		}
		if !g.occupied[i][j] {
			g.occupied[i][j] = true
			return
		}
	}
}

// ── planet generation tables (from makeplanet.c) ──────────────────────────

// xmin/xmax give the range of Maxx for each planet type.
var xmin = [8]int{15, 2, 4, 4, 26, 12, 12, 12}
var xmax = [8]int{23, 4, 8, 8, 32, 20, 20, 20}

// Initial sector condition per planet type (index = planet type).
// cond[] in makeplanet.c: SEA MOUNT LAND ICE GAS SEA FOREST DESERT
var initCond = [8]uint8{
	model.SectorSea,    // Earth
	model.SectorMount,  // Asteroid
	model.SectorLand,   // Mars
	model.SectorIce,    // Iceball
	model.SectorGas,    // GasGiant
	model.SectorSea,    // Water
	model.SectorForest, // Forest
	model.SectorDesert, // Desert
}

// fertility min/max per [planet type][sector type] — 7 sector types (no plated)
var fomin = [8][7]int{
	{25, 20, 10, 0, 20, 45, 5},  // Earth
	{0, 1, 2, 0, 0, 0, 1},       // Asteroid
	{0, 3, 2, 0, 0, 0, 2},       // Mars
	{0, 0, 8, 0, 25, 0, 0},      // Iceball
	{0, 0, 0, 35, 0, 0, 0},      // GasGiant
	{30, 0, 0, 0, 20, 0, 0},     // Water
	{30, 25, 20, 0, 30, 60, 15}, // Forest
	{0, 5, 2, 0, 0, 0, 2},       // Desert
}

var fomax = [8][7]int{
	{40, 35, 20, 0, 40, 65, 15}, // Earth
	{0, 2, 3, 0, 0, 0, 2},       // Asteroid
	{0, 5, 4, 0, 0, 0, 3},       // Mars
	{0, 0, 15, 0, 35, 0, 0},     // Iceball
	{0, 0, 0, 55, 0, 0, 0},      // GasGiant
	{50, 0, 0, 0, 40, 0, 0},     // Water
	{60, 45, 30, 0, 50, 90, 25}, // Forest
	{0, 10, 6, 0, 0, 0, 8},      // Desert
}

// resource min/max per [planet type][sector type]
var rmin = [8][7]int{
	{200, 225, 300, 0, 200, 0, 250}, // Earth
	{0, 250, 350, 0, 0, 0, 300},     // Asteroid
	{0, 225, 275, 0, 0, 0, 250},     // Mars
	{0, 0, 250, 0, 225, 0, 0},       // Iceball
	{0, 0, 0, 30, 0, 0, 0},          // GasGiant
	{175, 0, 0, 0, 200, 0, 0},       // Water
	{150, 0, 0, 0, 150, 150, 0},     // Forest
	{0, 200, 300, 0, 0, 0, 250},     // Desert
}

var rmax = [8][7]int{
	{250, 325, 400, 0, 250, 0, 300}, // Earth
	{0, 300, 600, 0, 0, 0, 400},     // Asteroid
	{0, 300, 500, 0, 0, 0, 300},     // Mars
	{0, 0, 350, 0, 300, 0, 0},       // Iceball
	{0, 0, 0, 60, 0, 0, 0},          // GasGiant
	{225, 0, 0, 0, 250, 0, 0},       // Water
	{250, 0, 0, 0, 250, 200, 0},     // Forest
	{0, 200, 500, 0, 0, 0, 350},     // Desert
}

// crystal chance per sector type (out of 1000)
var xChance = [7]int{5, 15, 10, 4, 5, 7, 6}

// ── atmosphere helpers ────────────────────────────────────────────────────

func (g *generator) makeEarthAtmosphere(p *model.Planet, chance int) {
	atmos := 100
	if g.intRand(0, 99) > chance {
		// oxygen-reducing atmosphere
		p.Conditions[model.AtmosOxygen] = i16(g.intRand(10, 25))
		atmos -= int(p.Conditions[model.AtmosOxygen])
		p.Conditions[model.AtmosNitrogen] = i16(g.intRand(20, atmos-20))
		atmos -= int(p.Conditions[model.AtmosNitrogen])
		p.Conditions[model.AtmosCO2] = i16(g.intRand(10, atmos/2))
		atmos -= int(p.Conditions[model.AtmosCO2])
		p.Conditions[model.AtmosHelium] = i16(g.intRand(2, atmos/8+1))
		atmos -= int(p.Conditions[model.AtmosHelium])
		p.Conditions[model.AtmosMethane] = i16(g.rng.Intn(2))
		atmos -= int(p.Conditions[model.AtmosMethane])
		p.Conditions[model.AtmosSulfur] = 0
		p.Conditions[model.AtmosHydrogen] = 0
		p.Conditions[model.AtmosOther] = i16(atmos)
	} else {
		// methane atmosphere
		p.Conditions[model.AtmosMethane] = i16(g.intRand(70, 80))
		atmos -= int(p.Conditions[model.AtmosMethane])
		p.Conditions[model.AtmosHydrogen] = i16(g.intRand(1, atmos/2))
		atmos -= int(p.Conditions[model.AtmosHydrogen])
		p.Conditions[model.AtmosHelium] = i16(1 + g.rng.Intn(2))
		atmos -= int(p.Conditions[model.AtmosHelium])
		p.Conditions[model.AtmosOxygen] = 0
		p.Conditions[model.AtmosCO2] = i16(1 + g.rng.Intn(2))
		atmos -= int(p.Conditions[model.AtmosCO2])
		p.Conditions[model.AtmosSulfur] = i16(g.rng.Intn(2))
		atmos -= int(p.Conditions[model.AtmosSulfur])
		p.Conditions[model.AtmosNitrogen] = i16(g.intRand(1, atmos/2))
		atmos -= int(p.Conditions[model.AtmosNitrogen])
		p.Conditions[model.AtmosOther] = i16(atmos)
	}
}

// ── sector map helpers ────────────────────────────────────────────────────

func sectorAt(p *model.Planet, x, y int) *model.Sector {
	return &p.Sectors[x+y*int(p.MaxX)]
}

// neighbors returns the number of adjacent sectors of the given type.
// Mirrors neighbors() in makeplanet.c (with wrap-around on X axis).
func neighbors(p *model.Planet, x, y int, stype uint8) int {
	maxx := int(p.MaxX)
	maxy := int(p.MaxY)
	l := x - 1
	r := x + 1
	if x == 0 {
		l = maxx - 1
	} else if r == maxx {
		r = 0
	}
	n := 0
	if y > 0 {
		if sectorAt(p, x, y-1).Type == stype {
			n++
		}
		if sectorAt(p, l, y-1).Type == stype {
			n++
		}
		if sectorAt(p, r, y-1).Type == stype {
			n++
		}
	}
	if sectorAt(p, l, y).Type == stype {
		n++
	}
	if sectorAt(p, r, y).Type == stype {
		n++
	}
	if y < maxy-1 {
		if sectorAt(p, x, y+1).Type == stype {
			n++
		}
		if sectorAt(p, l, y+1).Type == stype {
			n++
		}
		if sectorAt(p, r, y+1).Type == stype {
			n++
		}
	}
	return n
}

// seed randomly places n sectors of the given type.
func (g *generator) seedPlanet(p *model.Planet, stype uint8, n int) {
	for ; n > 0; n-- {
		x := g.intRand(0, int(p.MaxX)-1)
		y := g.intRand(0, int(p.MaxY)-1)
		s := sectorAt(p, x, y)
		s.Type = stype
		s.Condition = stype
	}
}

// grow spreads stype to any sector that has ≥ rate neighbours of that type.
// Mirrors grow() in makeplanet.c.
func growPlanet(p *model.Planet, stype uint8, n, rate int) {
	maxx := int(p.MaxX)
	maxy := int(p.MaxY)
	for ; n > 0; n-- {
		// Work on a copy so this pass is simultaneous.
		tmp := make([]model.Sector, len(p.Sectors))
		copy(tmp, p.Sectors)
		for x := 0; x < maxx; x++ {
			for y := 0; y < maxy; y++ {
				if neighbors(p, x, y, stype) >= rate {
					idx := x + y*maxx
					tmp[idx].Type = stype
					tmp[idx].Condition = stype
				}
			}
		}
		copy(p.Sectors, tmp)
	}
}

// sectTemp returns the temperature at latitude y on a planet.
// Mirrors SectTemp() in makeplanet.c.
func sectTemp(p *model.Planet, y int) int {
	const tfac = 10
	temp := int(p.Conditions[model.AtmosTemp])
	mid := (int(p.MaxY)+1)/2 - 1
	dy := y - mid
	if dy < 0 {
		dy = -dy
	}
	return temp - tfac*dy*dy
}

// temperature converts stellar distance + star temperature to planet temp.
// Mirrors Temperature() in makestar.c.
func temperature(dist float64, stemp uint8) int {
	return -269 + int(stemp)*1315*40/(40+int(dist))
}

// makesurface finalises resource/fert/crystal for every sector.
// Mirrors Makesurface() in makeplanet.c.
func (g *generator) makesurface(p *model.Planet) {
	for x := 0; x < int(p.MaxX); x++ {
		for y := 0; y < int(p.MaxY); y++ {
			s := sectorAt(p, x, y)
			temp := sectTemp(p, y)
			switch s.Type {
			case model.SectorSea:
				if temp < 0 && g.rng.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
					s.Condition = model.SectorIce
				}
			case model.SectorLand:
				if p.Type == u8(model.PlanetEarth) {
					if temp < 0 && g.rng.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
						s.Condition = model.SectorIce
					}
				}
			case model.SectorForest:
				if p.Type == u8(model.PlanetForest) {
					if temp < 0 && g.rng.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
						s.Condition = model.SectorIce
					}
				}
			}
			s.Type = s.Condition
			pt := int(p.Type)
			st := int(s.Type)
			if st < 7 {
				lo := rmin[pt][st]
				hi := rmax[pt][st]
				if hi > lo {
					s.Resource = u16(g.intRand(lo, hi))
				}
				flo := fomin[pt][st]
				fhi := fomax[pt][st]
				if fhi > flo {
					s.Fert = u8(g.intRand(flo, fhi))
				}
				if g.rng.Intn(1000) < xChance[st] {
					s.Crystals = u8(g.intRand(4, 8))
				}
			}
		}
	}
}

// ── planet constructor ────────────────────────────────────────────────────

// makePlanet generates one planet at the given distance from the star.
// Mirrors Makeplanet() in makeplanet.c.
func (g *generator) makePlanet(dist float64, stemp uint8, ptype int) model.Planet {
	var p model.Planet
	p.Type = u8(ptype)
	p.ExplTimer = 5

	t := i16(temperature(dist, stemp))
	p.Conditions[model.AtmosRTemp] = t
	p.Conditions[model.AtmosTemp] = t

	p.MaxX = u8(g.intRand(xmin[ptype], xmax[ptype]))
	f := float64(p.MaxX) / g.ratioXY
	maxy := g.roundRand(f) + 1
	if maxy%2 == 0 {
		maxy++
	}
	if ptype == model.PlanetAsteroid {
		maxy = g.intRand(1, 3)
	}
	p.MaxY = u8(maxy)
	p.Sectors = make([]model.Sector, int(p.MaxX)*int(p.MaxY))

	initType := initCond[ptype]
	for i := range p.Sectors {
		p.Sectors[i].Type = initType
		p.Sectors[i].Condition = initType
	}

	totalSects := (int(p.MaxY) - 1) * (int(p.MaxX) - 1)

	switch ptype {
	case model.PlanetGasGiant:
		atmos := 100
		if g.rng.Intn(2) == 0 { // methane planet
			p.Conditions[model.AtmosMethane] = i16(g.intRand(70, 80))
			atmos -= int(p.Conditions[model.AtmosMethane])
			p.Conditions[model.AtmosHydrogen] = i16(g.intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosHydrogen])
			p.Conditions[model.AtmosHelium] = 1
			atmos -= 1
			p.Conditions[model.AtmosOxygen] = 0
			p.Conditions[model.AtmosCO2] = 1
			atmos -= 1
			p.Conditions[model.AtmosNitrogen] = i16(g.intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			p.Conditions[model.AtmosSulfur] = 0
			p.Conditions[model.AtmosOther] = i16(atmos)
		} else {
			p.Conditions[model.AtmosHydrogen] = i16(g.intRand(30, 75))
			atmos -= int(p.Conditions[model.AtmosHydrogen])
			p.Conditions[model.AtmosHelium] = i16(g.intRand(20, atmos/2))
			atmos -= int(p.Conditions[model.AtmosHelium])
			p.Conditions[model.AtmosMethane] = i16(g.rng.Intn(2))
			atmos -= int(p.Conditions[model.AtmosMethane])
			p.Conditions[model.AtmosOxygen] = 0
			p.Conditions[model.AtmosCO2] = i16(g.rng.Intn(2))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = i16(g.intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			p.Conditions[model.AtmosSulfur] = 0
			p.Conditions[model.AtmosOther] = i16(atmos)
		}

	case model.PlanetMars:
		if g.rng.Intn(2) == 0 {
			atmos := 100
			p.Conditions[model.AtmosCO2] = i16(g.intRand(30, 45))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = i16(g.intRand(10, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			if g.rng.Intn(2) == 0 {
				p.Conditions[model.AtmosSulfur] = 0
			} else {
				p.Conditions[model.AtmosSulfur] = i16(g.intRand(20, atmos/2))
				atmos -= int(p.Conditions[model.AtmosSulfur])
			}
			p.Conditions[model.AtmosOther] = i16(atmos)
		}
		g.seedPlanet(&p, model.SectorDesert, g.intRand(1, totalSects))
		g.seedPlanet(&p, model.SectorMount, g.intRand(1, totalSects))

	case model.PlanetAsteroid:
		// Randomly flip some sectors to LAND.
		// The C code uses int_rand(1, Maxx) into a padded global buffer; here
		// we clamp to valid indices instead.
		for y := 0; y < int(p.MaxY); y++ {
			for x := 0; x < int(p.MaxX); x++ {
				if g.intRand(0, 3) == 0 {
					rx := g.intRand(0, int(p.MaxX)-1)
					ry := g.intRand(0, int(p.MaxY)-1)
					s := sectorAt(&p, rx, ry)
					s.Type = model.SectorLand
					s.Condition = model.SectorLand
				}
			}
		}
		g.seedPlanet(&p, model.SectorDesert, g.intRand(1, totalSects))

	case model.PlanetIceball:
		if int(p.MaxX)*int(p.MaxY) > g.intRand(0, 20) {
			atmos := 100
			p.Conditions[model.AtmosCO2] = i16(g.intRand(30, 45))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = i16(g.intRand(10, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			if g.rng.Intn(2) == 0 {
				p.Conditions[model.AtmosSulfur] = 0
			} else {
				p.Conditions[model.AtmosSulfur] = i16(g.intRand(20, atmos/2))
				atmos -= int(p.Conditions[model.AtmosSulfur])
			}
			p.Conditions[model.AtmosOther] = i16(atmos)
		}
		if totalSects/2 >= 1 {
			g.seedPlanet(&p, model.SectorMount, g.intRand(1, totalSects/2))
		}

	case model.PlanetEarth:
		g.makeEarthAtmosphere(&p, 33)
		lo, hi := totalSects/30, totalSects/20
		if hi < lo {
			hi = lo + 1
		}
		g.seedPlanet(&p, model.SectorLand, g.intRand(lo, hi))
		growPlanet(&p, model.SectorLand, 1, 1)
		growPlanet(&p, model.SectorLand, 1, 2)
		growPlanet(&p, model.SectorLand, 2, 3)
		growPlanet(&p, model.SectorSea, 1, 4)

	case model.PlanetForest:
		g.makeEarthAtmosphere(&p, 0)
		lo, hi := totalSects/30, totalSects/20
		if hi < lo {
			hi = lo + 1
		}
		g.seedPlanet(&p, model.SectorSea, g.intRand(lo, hi))
		growPlanet(&p, model.SectorSea, 1, 1)
		growPlanet(&p, model.SectorSea, 1, 3)
		growPlanet(&p, model.SectorForest, 1, 3)

	case model.PlanetWater:
		g.makeEarthAtmosphere(&p, 25)

	case model.PlanetDesert:
		g.makeEarthAtmosphere(&p, 50)
		lo, hi := totalSects/50, totalSects/25
		if hi < lo {
			hi = lo + 1
		}
		g.seedPlanet(&p, model.SectorMount, g.intRand(lo, hi))
		growPlanet(&p, model.SectorMount, 1, 1)
		growPlanet(&p, model.SectorMount, 1, 2)
		g.seedPlanet(&p, model.SectorLand, g.intRand(lo, hi))
		growPlanet(&p, model.SectorLand, 1, 1)
		growPlanet(&p, model.SectorLand, 1, 3)
		growPlanet(&p, model.SectorDesert, 1, 3)
	}

	g.makesurface(&p)
	return p
}

// ── star constructor ──────────────────────────────────────────────────────

// selectPlanetType picks a planet type based on temperature band.
// Mirrors the type-selection switch in Makestar() in makestar.c.
func (g *generator) selectPlanetType(temp, roll int) int {
	switch {
	case g.intRand(1, 100) <= 10 || temp > 400:
		return model.PlanetAsteroid
	case temp > 100 && temp <= 400:
		if roll <= 60 {
			return model.PlanetMars
		}
		return model.PlanetDesert
	case temp > 30 && temp <= 100:
		switch {
		case roll <= 25:
			return model.PlanetEarth
		case roll <= 50:
			return model.PlanetWater
		case roll <= 80:
			return model.PlanetForest
		case roll <= 90:
			return model.PlanetDesert
		default:
			return model.PlanetMars
		}
	case temp > -10 && temp <= 30:
		switch {
		case roll <= 45:
			return model.PlanetEarth
		case roll <= 70:
			return model.PlanetWater
		case roll <= 95:
			return model.PlanetForest
		default:
			return model.PlanetDesert
		}
	case temp > -50 && temp <= -10:
		switch {
		case roll <= 30:
			return model.PlanetDesert
		case roll <= 60:
			return model.PlanetIceball
		case roll <= 90:
			return model.PlanetForest
		default:
			return model.PlanetMars
		}
	case temp > -100 && temp <= -50:
		if roll <= 50 {
			return model.PlanetGasGiant
		}
		if roll <= 80 {
			return model.PlanetIceball
		}
		return model.PlanetMars
	default: // temp <= -100
		if roll <= 80 {
			return model.PlanetIceball
		}
		return model.PlanetGasGiant
	}
}

// makeStar generates one star with all its planets.
// Mirrors Makestar() in makestar.c.
func (g *generator) makeStar(starNum uint8, minPlanets, maxPlanets int) (*model.Star, []model.Planet) {
	star := &model.Star{Number: starNum}
	// gravity: layered random sums mirroring C expression
	star.Gravity = float64(g.intRand(0, g.intRand(0, 300))) +
		float64(g.intRand(0, 300)) +
		float64(g.intRand(100, 400)) +
		float64(g.intRand(0, 9))/10.0
	star.Temperature = u8(g.roundRand(star.Gravity / 100.0))
	star.Name = g.nextStarName()
	g.placeStar(star)

	nplanets := g.intRand(minPlanets, maxPlanets)
	star.NumPlanets = u8(nplanets)

	const planetDistMax = 1900.0
	const planetDistMin = 100.0

	var planets []model.Planet
	distmin := planetDistMin
	for i := 0; i < nplanets; i++ {
		distsep := (planetDistMax - distmin) / float64(nplanets-i)
		distmax := distmin + distsep
		dist := distmin + g.doubleRand()*(distmax-distmin)
		distmin = dist

		temp := temperature(dist, star.Temperature)
		angle := 2.0 * math.Pi * g.doubleRand()
		xpos := dist * math.Sin(angle)
		ypos := dist * math.Cos(angle)

		roll := g.intRand(1, 100)
		ptype := g.selectPlanetType(temp, roll)

		name := g.nextPlanetName(i)
		if i < len(star.PlanetNames) {
			star.PlanetNames[i] = name
		}

		planet := g.makePlanet(dist, star.Temperature, ptype)
		planet.StarNum = starNum
		planet.PlanetNum = u8(i)
		planet.XPos = xpos
		planet.YPos = ypos

		// Sum total resources
		var totalRes uint64
		for _, s := range planet.Sectors {
			totalRes += uint64(s.Resource)
		}
		planet.TotalResources = totalRes

		planets = append(planets, planet)
	}

	return star, planets
}
