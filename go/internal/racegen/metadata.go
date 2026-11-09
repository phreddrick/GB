package racegen

type NumberKind string

const (
	Decimal NumberKind = "decimal"
	Integer NumberKind = "integer"
	Boolean NumberKind = "boolean"
)

type AttributeDefinition struct {
	ID      Attribute  `json:"id"`
	Key     string     `json:"key"`
	Name    string     `json:"name"`
	Minimum float64    `json:"minimum"`
	Default float64    `json:"default"`
	Maximum float64    `json:"maximum"`
	Kind    NumberKind `json:"kind"`
}

// AttributeDefinitions returns independent metadata copies in C order.
func AttributeDefinitions() [AttributeCount]AttributeDefinition {
	return [AttributeCount]AttributeDefinition{
		{Adventurism, "adventurism", "Adventurism", .05, .4, .99, Decimal},
		{Absorb, "absorb", "Absorb", 0, 0, 1, Boolean},
		{Birthrate, "birthrate", "Birthrate", .2, .6, 1, Decimal},
		{CollectiveIQ, "collective_iq", "Collective IQ", 0, 0, 1, Boolean},
		{Fertilize, "fertilize", "Fertilize", 0, 0, 1, Decimal},
		{IQ, "iq", "IQ", 50, 150, 220, Integer},
		{Fighters, "fighters", "Fight", 1, 4, 20, Integer},
		{Pods, "pods", "Pods", 0, 0, 1, Boolean},
		{Mass, "mass", "Mass", .1, 1, 3, Decimal},
		{Sexes, "sexes", "Sexes", 1, 2, 53, Integer},
		{Metabolism, "metabolism", "Metabolism", .1, 1, 4, Decimal},
	}
}

type HomeworldDefinition struct {
	ID            Homeworld         `json:"id"`
	Name          string            `json:"name"`
	PlanetType    uint8             `json:"planet_type"`
	Cost          int               `json:"cost"`
	CommonSectors [SectorCount]bool `json:"common_sectors"`
}

// Homeworlds uses planet_translate from source/GB_racegen.c:30, including
// Airless -> Mars (2), not Asteroid (1). Common means planet covariance == 1.
func Homeworlds() [HomeworldCount]HomeworldDefinition {
	h := [HomeworldCount]HomeworldDefinition{
		{ID: Earth, Name: "Earth", PlanetType: 0, Cost: 75},
		{ID: Forest, Name: "Forest", PlanetType: 6, Cost: 50},
		{ID: Desert, Name: "Desert", PlanetType: 7, Cost: 50},
		{ID: Water, Name: "Water", PlanetType: 5, Cost: 50},
		{ID: Airless, Name: "Airless", PlanetType: 2, Cost: -25},
		{ID: Iceball, Name: "Iceball", PlanetType: 3, Cost: -25},
		{ID: Jovian, Name: "Jovian", PlanetType: 4, Cost: 600},
	}
	for i := range h {
		for j, c := range planetCovariances()[i] {
			h[i].CommonSectors[j] = c == 1
		}
	}
	return h
}

type SectorDefinition struct {
	ID      Sector     `json:"id"`
	Name    string     `json:"name"`
	Minimum float64    `json:"minimum"`
	Maximum float64    `json:"maximum"`
	Kind    NumberKind `json:"kind"`
}

func Sectors() [SectorCount]SectorDefinition {
	return [SectorCount]SectorDefinition{
		{Sea, "Water", 0, 100, Integer}, {Land, "Land", 0, 100, Integer},
		{Mountain, "Mountain", 0, 100, Integer}, {Gas, "Gas", 0, 100, Integer},
		{Ice, "Ice", 0, 100, Integer}, {Woods, "Forest", 0, 100, Integer},
		{Sand, "Desert", 0, 100, Integer}, {Plated, "Plated", 0, 100, Integer},
	}
}

// All tables are returned by value. No caller can mutate shared rules.
func planetCovariances() [HomeworldCount][SectorCount]float64 {
	return [HomeworldCount][SectorCount]float64{
		{1, 1, 2, 99, 1.01, 1.5, 3, 1.01},
		{1.01, 1.5, 2, 99, 1.01, 1, 3, 1.01},
		{3, 1.01, 1.01, 99, 1.5, 3, 1, 1.01},
		{1, 1.5, 3, 99, 1.01, 1.01, 3, 1.01},
		{1.01, 1, 1, 99, 1.01, 1.01, 1, 1.01},
		{3, 1.01, 1, 99, 1, 1.5, 2, 1.01},
		{99, 99, 99, 1, 99, 99, 99, 99},
	}
}

func sectorCovariances() [SectorCount][SectorCount]float64 {
	return [SectorCount][SectorCount]float64{
		{}, {0: .001}, {0: .002, 1: -.0005}, {0: 999, 1: 999, 2: 999},
		{0: .001, 2: -.002, 3: 999}, {1: -.001, 3: 999, 4: .001},
		{0: .003, 1: -.0005, 3: 999, 5: .001}, {3: 999},
	}
}
