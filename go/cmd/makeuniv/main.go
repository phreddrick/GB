// makeuniv generates a new Galactic Bloodshed universe and writes it to the
// bbolt database.  It mirrors the logic in source/makeuniv.c,
// source/makestar.c, and source/makeplanet.c.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// ── CLI flags ──────────────────────────────────────────────────────────────

var (
	flagStars      = flag.Int("s", -1, "number of stars (1–255)")
	flagMinPlanets = flag.Int("l", -1, "minimum planets per system (1–10)")
	flagMaxPlanets = flag.Int("m", -1, "maximum planets per system (≥ min, ≤ 10)")
	flagEmptyPct   = flag.Int("e", 0, "percent chance a star has no planets")
	flagAutoStar   = flag.Bool("a", false, "auto-load star names from star list file")
	flagAutoPlan   = flag.Bool("b", false, "auto-load planet names from planet list file")
	flagStarList   = flag.String("starlist", "starlist", "path to star name list file")
	flagPlanetList = flag.String("planlist", "planlist", "path to planet name list file")
	flagDB         = flag.String("db", "./gb.db", "path to bbolt database file")
	flagVerbose    = flag.Bool("v", false, "print planet info")
	flagStarInfo   = flag.Bool("w", false, "print star info")
)

// ── name list state ────────────────────────────────────────────────────────

var (
	cfg            *config.Config // Global config (initialized in main)
	nonInteractive bool           // True when all flags provided
	starNames      []string
	planetNames    []string
	starOrder      []int
	planetOrder    []int
	starIdx        int
	planetIdx      int
	autoStar       bool
	autoPlan       bool
)

// isInteractive checks if stdin is connected to a terminal.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// readNameList reads up to max names (one per line) from filename.
func readNameList(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			names = append(names, line)
		}
	}
	return names, sc.Err()
}

// randList produces a randomised permutation of [0, n].
// Mirrors rand_list() in makestar.c.
func randList(n int) []int {
	nums := make([]int, n+1)
	list := make([]int, n+1)
	for j := 0; j <= n; j++ {
		i := intRand(0, n)
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

func nextStarName() string {
	if autoStar && starIdx < len(starNames) {
		name := starNames[starOrder[starIdx]]
		starIdx++
		return name
	}
	// In interactive mode, prompt; otherwise use default
	if !nonInteractive {
		fmt.Printf("Next star name: ")
		var name string
		fmt.Scanln(&name)
		return name
	}
	// Default name for non-interactive mode
	name := fmt.Sprintf("Star-%d", starIdx+1)
	starIdx++
	return name
}

func nextPlanetName(i int) string {
	numbers := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}
	if autoPlan && planetIdx < len(planetNames) {
		name := planetNames[planetOrder[planetIdx]]
		planetIdx++
		return name
	}
	if i < len(numbers) {
		return numbers[i]
	}
	return fmt.Sprintf("%d", i+1)
}

// ── random helpers (self-contained so makeuniv doesn't import game/) ───────

func intRand(low, hi int) int {
	if hi <= low {
		return low
	}
	return low + rand.Intn(hi-low+1)
}

func doubleRand() float64 {
	return rand.Float64()
}

func roundRand(x float64) int {
	if x < 0 {
		return int(x)
	}
	fl := int(x)
	if doubleRand() > x-float64(fl) {
		return fl
	}
	return fl + 1
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

// ratioXY is loaded from config at startup (was hardcoded to 2.6, should be 3.70)

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

func makeEarthAtmosphere(p *model.Planet, chance int) {
	atmos := 100
	if intRand(0, 99) > chance {
		// oxygen-reducing atmosphere
		p.Conditions[model.AtmosOxygen] = int16(intRand(10, 25))
		atmos -= int(p.Conditions[model.AtmosOxygen])
		p.Conditions[model.AtmosNitrogen] = int16(intRand(20, atmos-20))
		atmos -= int(p.Conditions[model.AtmosNitrogen])
		p.Conditions[model.AtmosCO2] = int16(intRand(10, atmos/2))
		atmos -= int(p.Conditions[model.AtmosCO2])
		p.Conditions[model.AtmosHelium] = int16(intRand(2, atmos/8+1))
		atmos -= int(p.Conditions[model.AtmosHelium])
		p.Conditions[model.AtmosMethane] = int16(rand.Intn(2))
		atmos -= int(p.Conditions[model.AtmosMethane])
		p.Conditions[model.AtmosSulfur] = 0
		p.Conditions[model.AtmosHydrogen] = 0
		p.Conditions[model.AtmosOther] = int16(atmos)
	} else {
		// methane atmosphere
		p.Conditions[model.AtmosMethane] = int16(intRand(70, 80))
		atmos -= int(p.Conditions[model.AtmosMethane])
		p.Conditions[model.AtmosHydrogen] = int16(intRand(1, atmos/2))
		atmos -= int(p.Conditions[model.AtmosHydrogen])
		p.Conditions[model.AtmosHelium] = int16(1 + rand.Intn(2))
		atmos -= int(p.Conditions[model.AtmosHelium])
		p.Conditions[model.AtmosOxygen] = 0
		p.Conditions[model.AtmosCO2] = int16(1 + rand.Intn(2))
		atmos -= int(p.Conditions[model.AtmosCO2])
		p.Conditions[model.AtmosSulfur] = int16(rand.Intn(2))
		atmos -= int(p.Conditions[model.AtmosSulfur])
		p.Conditions[model.AtmosNitrogen] = int16(intRand(1, atmos/2))
		atmos -= int(p.Conditions[model.AtmosNitrogen])
		p.Conditions[model.AtmosOther] = int16(atmos)
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
func seedPlanet(p *model.Planet, stype uint8, n int) {
	for ; n > 0; n-- {
		x := intRand(0, int(p.MaxX)-1)
		y := intRand(0, int(p.MaxY)-1)
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
func makesurface(p *model.Planet) {
	for x := 0; x < int(p.MaxX); x++ {
		for y := 0; y < int(p.MaxY); y++ {
			s := sectorAt(p, x, y)
			temp := sectTemp(p, y)
			switch s.Type {
			case model.SectorSea:
				if temp < 0 && rand.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
					s.Condition = model.SectorIce
				}
			case model.SectorLand:
				if p.Type == uint8(model.PlanetEarth) {
					if temp < 0 && rand.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
						s.Condition = model.SectorIce
					}
				}
			case model.SectorForest:
				if p.Type == uint8(model.PlanetForest) {
					if temp < 0 && rand.Intn(100) < -temp && (y == 0 || y == int(p.MaxY)-1) {
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
					s.Resource = uint16(intRand(lo, hi))
				}
				flo := fomin[pt][st]
				fhi := fomax[pt][st]
				if fhi > flo {
					s.Fert = uint8(intRand(flo, fhi))
				}
				if rand.Intn(1000) < xChance[st] {
					s.Crystals = uint8(intRand(4, 8))
				}
			}
		}
	}
}

// ── planet constructor ────────────────────────────────────────────────────

// makePlanet generates one planet at the given distance from the star.
// Mirrors Makeplanet() in makeplanet.c.
func makePlanet(dist float64, stemp uint8, ptype int) model.Planet {
	var p model.Planet
	p.Type = uint8(ptype)
	p.ExplTimer = 5

	t := int16(temperature(dist, stemp))
	p.Conditions[model.AtmosRTemp] = t
	p.Conditions[model.AtmosTemp] = t

	p.MaxX = uint8(intRand(xmin[ptype], xmax[ptype]))
	f := float64(p.MaxX) / cfg.RatioXY
	maxy := roundRand(f) + 1
	if maxy%2 == 0 {
		maxy++
	}
	if ptype == model.PlanetAsteroid {
		maxy = intRand(1, 3)
	}
	p.MaxY = uint8(maxy)
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
		if rand.Intn(2) == 0 { // methane planet
			p.Conditions[model.AtmosMethane] = int16(intRand(70, 80))
			atmos -= int(p.Conditions[model.AtmosMethane])
			p.Conditions[model.AtmosHydrogen] = int16(intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosHydrogen])
			p.Conditions[model.AtmosHelium] = 1
			atmos -= 1
			p.Conditions[model.AtmosOxygen] = 0
			p.Conditions[model.AtmosCO2] = 1
			atmos -= 1
			p.Conditions[model.AtmosNitrogen] = int16(intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			p.Conditions[model.AtmosSulfur] = 0
			p.Conditions[model.AtmosOther] = int16(atmos)
		} else {
			p.Conditions[model.AtmosHydrogen] = int16(intRand(30, 75))
			atmos -= int(p.Conditions[model.AtmosHydrogen])
			p.Conditions[model.AtmosHelium] = int16(intRand(20, atmos/2))
			atmos -= int(p.Conditions[model.AtmosHelium])
			p.Conditions[model.AtmosMethane] = int16(rand.Intn(2))
			atmos -= int(p.Conditions[model.AtmosMethane])
			p.Conditions[model.AtmosOxygen] = 0
			p.Conditions[model.AtmosCO2] = int16(rand.Intn(2))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = int16(intRand(1, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			p.Conditions[model.AtmosSulfur] = 0
			p.Conditions[model.AtmosOther] = int16(atmos)
		}

	case model.PlanetMars:
		if rand.Intn(2) == 0 {
			atmos := 100
			p.Conditions[model.AtmosCO2] = int16(intRand(30, 45))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = int16(intRand(10, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			if rand.Intn(2) == 0 {
				p.Conditions[model.AtmosSulfur] = 0
			} else {
				p.Conditions[model.AtmosSulfur] = int16(intRand(20, atmos/2))
				atmos -= int(p.Conditions[model.AtmosSulfur])
			}
			p.Conditions[model.AtmosOther] = int16(atmos)
		}
		seedPlanet(&p, model.SectorDesert, intRand(1, totalSects))
		seedPlanet(&p, model.SectorMount, intRand(1, totalSects))

	case model.PlanetAsteroid:
		// Randomly flip some sectors to LAND.
		// The C code uses int_rand(1, Maxx) into a padded global buffer; here
		// we clamp to valid indices instead.
		for y := 0; y < int(p.MaxY); y++ {
			for x := 0; x < int(p.MaxX); x++ {
				if intRand(0, 3) == 0 {
					rx := intRand(0, int(p.MaxX)-1)
					ry := intRand(0, int(p.MaxY)-1)
					s := sectorAt(&p, rx, ry)
					s.Type = model.SectorLand
					s.Condition = model.SectorLand
				}
			}
		}
		seedPlanet(&p, model.SectorDesert, intRand(1, totalSects))

	case model.PlanetIceball:
		if int(p.MaxX)*int(p.MaxY) > intRand(0, 20) {
			atmos := 100
			p.Conditions[model.AtmosCO2] = int16(intRand(30, 45))
			atmos -= int(p.Conditions[model.AtmosCO2])
			p.Conditions[model.AtmosNitrogen] = int16(intRand(10, atmos/2))
			atmos -= int(p.Conditions[model.AtmosNitrogen])
			if rand.Intn(2) == 0 {
				p.Conditions[model.AtmosSulfur] = 0
			} else {
				p.Conditions[model.AtmosSulfur] = int16(intRand(20, atmos/2))
				atmos -= int(p.Conditions[model.AtmosSulfur])
			}
			p.Conditions[model.AtmosOther] = int16(atmos)
		}
		if totalSects/2 >= 1 {
			seedPlanet(&p, model.SectorMount, intRand(1, totalSects/2))
		}

	case model.PlanetEarth:
		makeEarthAtmosphere(&p, 33)
		lo, hi := totalSects/30, totalSects/20
		if hi < lo {
			hi = lo + 1
		}
		seedPlanet(&p, model.SectorLand, intRand(lo, hi))
		growPlanet(&p, model.SectorLand, 1, 1)
		growPlanet(&p, model.SectorLand, 1, 2)
		growPlanet(&p, model.SectorLand, 2, 3)
		growPlanet(&p, model.SectorSea, 1, 4)

	case model.PlanetForest:
		makeEarthAtmosphere(&p, 0)
		lo, hi := totalSects/30, totalSects/20
		if hi < lo {
			hi = lo + 1
		}
		seedPlanet(&p, model.SectorSea, intRand(lo, hi))
		growPlanet(&p, model.SectorSea, 1, 1)
		growPlanet(&p, model.SectorSea, 1, 3)
		growPlanet(&p, model.SectorForest, 1, 3)

	case model.PlanetWater:
		makeEarthAtmosphere(&p, 25)

	case model.PlanetDesert:
		makeEarthAtmosphere(&p, 50)
		lo, hi := totalSects/50, totalSects/25
		if hi < lo {
			hi = lo + 1
		}
		seedPlanet(&p, model.SectorMount, intRand(lo, hi))
		growPlanet(&p, model.SectorMount, 1, 1)
		growPlanet(&p, model.SectorMount, 1, 2)
		seedPlanet(&p, model.SectorLand, intRand(lo, hi))
		growPlanet(&p, model.SectorLand, 1, 1)
		growPlanet(&p, model.SectorLand, 1, 3)
		growPlanet(&p, model.SectorDesert, 1, 3)
	}

	makesurface(&p)
	return p
}

// ── star placement ────────────────────────────────────────────────────────

const univSize = 150000.0

var occupied [100][100]bool

// placeStar picks a random position for a star, avoiding already-occupied cells.
func placeStar(star *model.Star) {
	for {
		star.XPos = float64(intRand(int(-univSize), int(univSize)))
		star.YPos = float64(intRand(int(-univSize), int(univSize)))
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
		if !occupied[i][j] {
			occupied[i][j] = true
			return
		}
	}
}

// ── star constructor ──────────────────────────────────────────────────────

// selectPlanetType picks a planet type based on temperature band.
// Mirrors the type-selection switch in Makestar() in makestar.c.
func selectPlanetType(temp, roll int) int {
	switch {
	case intRand(1, 100) <= 10 || temp > 400:
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
func makeStar(starNum uint8, minPlanets, maxPlanets int, verbose, starInfo bool) (*model.Star, []model.Planet) {
	star := &model.Star{Number: starNum}
	// gravity: layered random sums mirroring C expression
	star.Gravity = float64(intRand(0, intRand(0, 300))) +
		float64(intRand(0, 300)) +
		float64(intRand(100, 400)) +
		float64(intRand(0, 9))/10.0
	star.Temperature = uint8(roundRand(star.Gravity / 100.0))
	star.Name = nextStarName()
	placeStar(star)

	if starInfo {
		fmt.Printf("Star %s: gravity %.1f, temp %d\n", star.Name, star.Gravity, star.Temperature)
	}

	nplanets := intRand(minPlanets, maxPlanets)
	star.NumPlanets = uint8(nplanets)

	const planetDistMax = 1900.0
	const planetDistMin = 100.0

	var planets []model.Planet
	distmin := planetDistMin
	for i := 0; i < nplanets; i++ {
		distsep := (planetDistMax - distmin) / float64(nplanets-i)
		distmax := distmin + distsep
		dist := distmin + doubleRand()*(distmax-distmin)
		distmin = dist

		temp := temperature(dist, star.Temperature)
		angle := 2.0 * math.Pi * doubleRand()
		xpos := dist * math.Sin(angle)
		ypos := dist * math.Cos(angle)

		roll := intRand(1, 100)
		ptype := selectPlanetType(temp, roll)

		name := nextPlanetName(i)
		if i < len(star.PlanetNames) {
			star.PlanetNames[i] = name
		}

		planet := makePlanet(dist, star.Temperature, ptype)
		planet.StarNum = starNum
		planet.PlanetNum = uint8(i)
		planet.XPos = xpos
		planet.YPos = ypos

		// Sum total resources
		var totalRes uint64
		for _, s := range planet.Sectors {
			totalRes += uint64(s.Resource)
		}
		planet.TotalResources = totalRes

		if verbose {
			typeName := []string{"Earth", "Asteroid", "Mars", "Iceball", "GasGiant", "Water", "Forest", "Desert"}[ptype]
			fmt.Printf("  Planet %s: temp %d, type %s, map %dx%d\n",
				name, planet.Conditions[model.AtmosRTemp], typeName, planet.MaxX, planet.MaxY)
		}

		planets = append(planets, planet)
	}

	return star, planets
}

// ── main ─────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	// Initialize config with defaults from tweakables.h
	cfg = config.Default()

	autoStar = *flagAutoStar
	autoPlan = *flagAutoPlan

	// Load name lists
	if autoStar {
		names, err := readNameList(*flagStarList)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot read star list %q: %v\n", *flagStarList, err)
			autoStar = false
		} else {
			starNames = names
			if len(starNames) > 0 {
				starOrder = randList(len(starNames) - 1)
				fmt.Printf("%d names listed in %s\n", len(starNames), *flagStarList)
			}
		}
	}
	if autoPlan {
		names, err := readNameList(*flagPlanetList)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot read planet list %q: %v\n", *flagPlanetList, err)
			autoPlan = false
		} else {
			planetNames = names
			if len(planetNames) > 0 {
				planetOrder = randList(len(planetNames) - 1)
				fmt.Printf("%d names listed in %s\n", len(planetNames), *flagPlanetList)
			}
		}
	}

	// Check if all universe parameters are provided via flags
	// If so, run non-interactively
	if *flagStars > 0 && *flagMinPlanets > 0 && *flagMaxPlanets > 0 {
		nonInteractive = true
	}

	// Use flag values, or prompt if not supplied
	nstars := *flagStars
	if nstars < 1 || nstars >= model.NumStars {
		// Try to read from stdin; if not a TTY, use default
		if !nonInteractive && isInteractive() {
			for nstars < 1 || nstars >= model.NumStars {
				fmt.Printf("Number of stars [1-%d]: ", model.NumStars-1)
				fmt.Scan(&nstars)
			}
		} else {
			nstars = 100 // Default for non-interactive
		}
	}

	minPlanets := *flagMinPlanets
	if minPlanets <= 0 || minPlanets > model.MaxPlanets {
		if !nonInteractive && isInteractive() {
			for minPlanets <= 0 || minPlanets > model.MaxPlanets {
				fmt.Printf("Minimum number of planets per system [1-%d]: ", model.MaxPlanets)
				fmt.Scan(&minPlanets)
			}
		} else {
			minPlanets = 2 // Default for non-interactive
		}
	}

	maxPlanets := *flagMaxPlanets
	if maxPlanets < minPlanets || maxPlanets > model.MaxPlanets {
		if !nonInteractive && isInteractive() {
			for maxPlanets < minPlanets || maxPlanets > model.MaxPlanets {
				fmt.Printf("Maximum number of planets per system [%d-%d]: ", minPlanets, model.MaxPlanets)
				fmt.Scan(&maxPlanets)
			}
		} else {
			maxPlanets = model.MaxPlanets // Default for non-interactive
		}
	}

	// Open database
	store, err := db.Open(*flagDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database %q: %v\n", *flagDB, err)
		os.Exit(1)
	}
	defer store.Close()

	fmt.Printf("Generating %d stars...\n", nstars)

	// Generate stars and planets
	sd := &model.StarData{NumStars: uint16(nstars)}
	if err := store.PutStarData(sd); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing StarData: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < nstars; i++ {
		star, planets := makeStar(uint8(i), minPlanets, maxPlanets, *flagVerbose, *flagStarInfo)
		if err := store.PutStar(star); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing star %d: %v\n", i, err)
			os.Exit(1)
		}
		for j := range planets {
			if err := store.PutPlanet(&planets[j]); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing planet %d/%d: %v\n", i, j, err)
				os.Exit(1)
			}
		}
		if (i+1)%10 == 0 || i+1 == nstars {
			fmt.Printf("  %d/%d stars generated\n", i+1, nstars)
		}
	}

	fmt.Printf("Universe generation complete: %d stars written to %s\n", nstars, *flagDB)
}
