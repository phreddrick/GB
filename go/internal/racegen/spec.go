// Package racegen implements pure species-design rules from source/racegen.c.
// It owns no credentials, permissions, placement, randomness, or persistence.
// Costs and validation are authoritative Go rules for both CLI and future API
// consumers; a browser-provided point total must never be trusted.
package racegen

const Budget = 1400

type RaceType int

const (
	Normal RaceType = iota
	Metamorph
)

type Homeworld int

const (
	Earth Homeworld = iota
	Forest
	Desert
	Water
	Airless
	Iceball
	Jovian
	HomeworldCount
)

type Sector int

const (
	Sea Sector = iota
	Land
	Mountain
	Gas
	Ice
	Woods
	Sand
	Plated
	SectorCount
)

type Attribute int

const (
	Adventurism Attribute = iota
	Absorb
	Birthrate
	CollectiveIQ
	Fertilize
	IQ
	Fighters
	Pods
	Mass
	Sexes
	Metabolism
	AttributeCount
)

// Attributes retain numeric boolean/integer inputs until validation, so an API
// can report fractional values as errors rather than silently truncate them.
// Fertilize and Adventurism are fractions, not percentages. SectorCompat below
// uses percentages. These are design values, not the world's model.Race fields.
type Attributes struct {
	Adventurism  float64 `json:"adventurism"`
	Absorb       float64 `json:"absorb"`
	Birthrate    float64 `json:"birthrate"`
	CollectiveIQ float64 `json:"collective_iq"`
	Fertilize    float64 `json:"fertilize"`
	IQ           float64 `json:"iq"`
	Fighters     float64 `json:"fighters"`
	Pods         float64 `json:"pods"`
	Mass         float64 `json:"mass"`
	Sexes        float64 `json:"sexes"`
	Metabolism   float64 `json:"metabolism"`
}

// Values returns a copy in original-C attribute order, also used by metadata.
func (a Attributes) Values() [AttributeCount]float64 {
	return [AttributeCount]float64{a.Adventurism, a.Absorb, a.Birthrate, a.CollectiveIQ, a.Fertilize, a.IQ, a.Fighters, a.Pods, a.Mass, a.Sexes, a.Metabolism}
}

func AttributesFromValues(v [AttributeCount]float64) Attributes {
	return Attributes{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9], v[10]}
}

// Spec is a design, never an authorization to enroll or create a God race.
// Address is optional metadata; credentials belong to a separate workflow.
// JSON tags describe field names, not a complete untrusted-input decoder.
// A future API must check presence/nulls, unknown fields, and exact array
// lengths before constructing Spec: encoding/json alone accepts null numeric
// fields as zero and discards excess fixed-array elements.
type Spec struct {
	Name         string               `json:"name"`
	Address      string               `json:"address,omitempty"`
	Info         string               `json:"info,omitempty"`
	RaceType     RaceType             `json:"race_type"`
	Homeworld    Homeworld            `json:"homeworld"`
	Attributes   Attributes           `json:"attributes"`
	SectorCompat [SectorCount]float64 `json:"sector_compat"`
}

// Default returns C's initial designer state. Like C, this is an unfinished
// draft: plated-only compatibility fails the full common-terrain validation.
func Default() Spec {
	var values [AttributeCount]float64
	for i, d := range AttributeDefinitions() {
		values[i] = d.Default
	}
	s := Spec{Name: "Unknown", Attributes: AttributesFromValues(values)}
	s.SectorCompat[Plated] = 100
	return s
}
