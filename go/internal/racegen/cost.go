package racegen

import "math"

// Original racegen formulas and tables: Copyright (c) Leonard Dickens 1991
// (leonard@cs.umd.edu). Permission to copy, distribute, and/or alter is granted
// as long as the copyright notice and these terms are left unchanged in all
// derivatives/copies. Anybody who does alter this program, please take credit!
// CR-07A Go adaptation: Galactic Bloodshed project contributors, 2026.

type formula struct{ exponential, scale, hinge, linear float64 }

func formulas() [AttributeCount]formula {
	return [AttributeCount]formula{
		{0, 0, 0, 300}, {0, 0, 0, 200}, {0, 0, 0, 500}, {0, 0, 0, -350.5},
		{200, 1, 1, 300}, {100, .03, 140, 6}, {10, .4, 6, 65}, {0, 0, 0, 200},
		{100, 1, 3.1, -100}, {2.2, -.5, 9, -3}, {300, 1, 1.3, 700},
	}
}

// Covariances starts with normal(), then applies metamorph()'s assignments.
// In particular, C retains the BIRTH/FIGHT/METAB x IQ terms for metamorphs.
// Invalid race types are rejected by Cost/Validate; this metadata accessor
// returns the normal matrix for unknown types and never indexes with them.
func Covariances(kind RaceType) [AttributeCount][AttributeCount]float64 {
	defs := AttributeDefinitions()
	r := func(i Attribute) float64 { return defs[i].Maximum - defs[i].Minimum }
	var c [AttributeCount][AttributeCount]float64
	c[Adventurism][IQ] = -.4 / r(IQ)
	c[Birthrate][IQ] = .2 / r(IQ)
	c[Birthrate][Mass] = .4 / r(Mass)
	c[Birthrate][Sexes] = .9 / r(Sexes)
	c[Birthrate][Metabolism] = -.2 / r(Metabolism)
	c[Fertilize][Metabolism] = -.2 / r(Metabolism)
	c[Fighters][IQ] = -.2 / r(IQ)
	c[Fighters][Adventurism] = -.05 / r(Adventurism)
	c[Fighters][Mass] = -.2 / r(Mass)
	c[Fighters][Metabolism] = -.05 / r(Metabolism)
	c[Metabolism][Mass] = .15 / r(Mass)
	c[Metabolism][IQ] = -.1 / r(IQ)
	c[IQ][Mass] = -.25 / r(Mass)
	if kind == Metamorph {
		c[Adventurism][IQ] = 0
		c[Birthrate][Mass] = .1 / r(Mass)
		c[Birthrate][Sexes] = .5 / r(Sexes)
		c[Birthrate][Metabolism] = -.1 / r(Metabolism)
		c[Fertilize][Metabolism] = -.3 / r(Metabolism)
		c[Fighters][Adventurism] = -.1 / r(Adventurism)
		c[Fighters][Mass] = -.2 / r(Mass)
		c[Fighters][Metabolism] = -.15 / r(Metabolism)
		c[Metabolism][Mass] = .05 / r(Mass)
		c[IQ][Mass] = 0
	}
	return c
}

func rawAttributes(v [AttributeCount]float64, c [AttributeCount][AttributeCount]float64) [AttributeCount]float64 {
	var costs [AttributeCount]float64
	for i, f := range formulas() {
		costs[i] = math.Exp(f.scale*(v[i]-f.hinge))*f.exponential + v[i]*f.linear
	}
	for i := range costs {
		for j, cov := range c[i] {
			if cov != 0 {
				costs[i] *= 1 + cov*v[j]
			}
		}
	}
	return costs
}

// Calibration is integer-valued, as initialize() calibrates from already
// ROUNDed normal default costs. It is not recomputed for metamorph designs.
func Calibration() [AttributeCount]int {
	var v [AttributeCount]float64
	for i, d := range AttributeDefinitions() {
		v[i] = d.Default
	}
	var fudge [AttributeCount]int
	for i, x := range rawAttributes(v, Covariances(Normal)) {
		fudge[i] = -int(math.Trunc(.5 + x))
	}
	return fudge
}

type Breakdown struct {
	Attributes         [AttributeCount]int `json:"attributes"`
	Sectors            [SectorCount]int    `json:"sectors"`
	Homeworld          int                 `json:"homeworld"`
	SectorCount        int                 `json:"sector_count"`
	SectorSurcharge    int                 `json:"sector_surcharge"`
	EightTypeExtension bool                `json:"eight_type_extension"`
	Total              int                 `json:"total"`
	Remaining          int                 `json:"remaining"`
}

// cRound implements ROUND(f) = (int)(0.5+f), including negative truncation.
// Original C uses a 32-bit int: reject overflow before any integer conversion.
func cRound(x float64) (int, bool) {
	y := math.Trunc(.5 + x)
	if !finite(y) || y < math.MinInt32 || y > math.MaxInt32 {
		return 0, false
	}
	return int(y), true
}

// Cost calculates a preview, not acceptance. It checks finite numbers, ranges
// and enum indices but permits fractional integral fields and cross-field
// violations so an unfinished design can be explained. Validate must succeed
// before a design is used. No numeric error returns a partial breakdown.
func Cost(s Spec) (Breakdown, Errors) {
	if errs := checkNumbers(s); len(errs) > 0 {
		return Breakdown{}, errs
	}
	var b Breakdown
	fudge := Calibration()
	for i, x := range rawAttributes(s.Attributes.Values(), Covariances(s.RaceType)) {
		n, ok := cRound(x + float64(fudge[i]))
		if !ok {
			return Breakdown{}, overflow(attributeField(i))
		}
		b.Attributes[i] = n
	}
	var sectorCosts [SectorCount]float64
	for i, x := range s.SectorCompat {
		if x != 0 {
			b.SectorCount++
		}
		sectorCosts[i] = x*.5 + 10.8*math.Log(1+x)
	}
	cov := sectorCovariances()
	for i := range sectorCosts {
		for j := i + 1; j < int(SectorCount); j++ {
			if cov[j][i] != 0 {
				sectorCosts[i] *= 1 + cov[j][i]*s.SectorCompat[j]
				sectorCosts[j] *= 1 + cov[j][i]*s.SectorCompat[i]
			}
		}
	}
	for i, x := range sectorCosts {
		if c := planetCovariances()[s.Homeworld][i]; c > 1.01 {
			x *= c
		}
		n, ok := cRound(x)
		if !ok {
			return Breakdown{}, overflow(sectorField(i))
		}
		b.Sectors[i] = n
	}
	b.Homeworld = Homeworlds()[s.Homeworld].Cost
	b.SectorSurcharge = [SectorCount + 1]int{-1, 0, 50, 100, 200, 300, 400, 500, 600}[b.SectorCount]
	b.EightTypeExtension = b.SectorCount == int(SectorCount)
	// Preserve C accumulation order and reject signed overflow (undefined in C).
	add := func(n int) bool {
		sum := int64(b.Total) + int64(n)
		if sum < math.MinInt32 || sum > math.MaxInt32 {
			return false
		}
		b.Total = int(sum)
		return true
	}
	for _, n := range b.Attributes {
		if !add(n) {
			return Breakdown{}, overflow("cost")
		}
	}
	if !add(b.Homeworld) {
		return Breakdown{}, overflow("cost")
	}
	for _, n := range b.Sectors {
		if !add(n) {
			return Breakdown{}, overflow("cost")
		}
	}
	if !add(b.SectorSurcharge) || int64(Budget)-int64(b.Total) > math.MaxInt32 {
		return Breakdown{}, overflow("cost")
	}
	b.Remaining = Budget - b.Total
	return b, nil
}
