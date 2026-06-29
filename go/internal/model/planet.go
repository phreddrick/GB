package model

// MerchantRoute holds one planet merchant shipping order.
// Corresponds to the anonymous struct inside plinfo.route[] in vars.h.
type MerchantRoute struct {
	Set        bool  `json:"set"`
	DestStar   uint8 `json:"dest_star"`
	DestPlanet uint8 `json:"dest_planet"`
	Load       uint8 `json:"load"`   // commodity bitmask
	Unload     uint8 `json:"unload"` // commodity bitmask
	X          uint8 `json:"x"`
	Y          uint8 `json:"y"`
}

// PlInfo holds per-player stockpiles and production data for one planet.
// Corresponds to struct plinfo in vars.h.
type PlInfo struct {
	Fuel     uint16 `json:"fuel"`
	Destruct uint16 `json:"destruct"`
	Resource uint16 `json:"resource"`
	Popn     uint32 `json:"popn"`
	Troops   uint32 `json:"troops"`
	Crystals uint16 `json:"crystals"`

	// Last-update production totals
	ProdRes      uint16  `json:"prod_res"`
	ProdFuel     uint16  `json:"prod_fuel"`
	ProdDest     uint16  `json:"prod_dest"`
	ProdCrystals uint16  `json:"prod_crystals"`
	ProdMoney    uint64  `json:"prod_money"`
	ProdTech     float64 `json:"prod_tech"`

	TechInvest    uint16 `json:"tech_invest"`
	NumSectsOwned uint16 `json:"num_sects_owned"`

	ComRead  uint8 `json:"com_read"`  // combat readiness / mobilization
	MobSet   uint8 `json:"mob_set"`   // mobilization target
	ToxThresh uint8 `json:"tox_thresh"` // min toxicity to build waste container

	Explored bool  `json:"explored"`
	AutoRep  uint8 `json:"auto_rep"`
	Tax      uint8 `json:"tax"`
	NewTax   uint8 `json:"new_tax"`
	Guns     uint8 `json:"guns"` // planetary gun count (mob/5)

	Route [MaxRoutes]MerchantRoute `json:"route"`

	MobPoints     uint64  `json:"mob_points"`
	EstProduction float64 `json:"est_production"`
}

// Sector represents one map sector on a planet.
// Corresponds to struct sector / sectortype in vars.h.
// Sectors are embedded as a flat array inside Planet.Sectors.
type Sector struct {
	Eff          uint8  `json:"eff"`
	Fert         uint8  `json:"fert"`
	Mobilization uint8  `json:"mobilization"`
	Crystals     uint8  `json:"crystals"`
	Resource     uint16 `json:"resource"`
	Popn         uint16 `json:"popn"`
	Troops       uint16 `json:"troops"`
	Owner        uint8  `json:"owner"`
	Race         uint8  `json:"race"`
	Type         uint8  `json:"type"`      // sector geology (SectorLand, etc.)
	Condition    uint8  `json:"condition"` // environmental condition
}

// Planet represents one planet in the game.
// Corresponds to struct planet / planettype in vars.h.
// Sectors are stored inline (replacing the binary sectormappos file offset).
type Planet struct {
	// Identity
	StarNum   uint8 `json:"star_num"`
	PlanetNum uint8 `json:"planet_num"`

	// Orbital position
	XPos float64 `json:"x_pos"`
	YPos float64 `json:"y_pos"`

	// First ship in orbit (linked list)
	Ships uint32 `json:"ships"`

	// Map dimensions
	MaxX uint8 `json:"max_x"`
	MaxY uint8 `json:"max_y"`

	// Per-player data
	Info [MaxPlayers]PlInfo `json:"info"`

	// Atmospheric conditions for terraforming (indexed by AtmosXxx constants)
	Conditions [AtmosToxic + 1]int16 `json:"conditions"`

	// Aggregate population / resources
	Popn           uint64 `json:"popn"`
	Troops         uint64 `json:"troops"`
	MaxPopn        uint64 `json:"max_popn"`
	TotalResources uint64 `json:"total_resources"`

	// Sector map (row-major: index = x + y*MaxX)
	// Stored inline; replaces the binary sectormappos offset.
	Sectors []Sector `json:"sectors"`

	SlavedTo  uint8 `json:"slaved_to"`
	Type      uint8 `json:"type"`       // PlanetEarth, PlanetMars, etc.
	ExplTimer uint8 `json:"expl_timer"`
	Explored  bool  `json:"explored"`
}

// SectorAt returns a pointer to the sector at (x, y).
func (p *Planet) SectorAt(x, y int) *Sector {
	return &p.Sectors[x+y*int(p.MaxX)]
}

// Commodity represents one market commodity lot.
// Corresponds to struct commod / commodtype in vars.h.
type Commodity struct {
	Number     uint32 `json:"number"`
	Owner      uint8  `json:"owner"`
	Governor   uint8  `json:"governor"`
	Type       uint8  `json:"type"` // CommodResource etc.
	Amount     uint16 `json:"amount"`
	Deliver    bool   `json:"deliver"` // ready for shipping
	Bid        uint64 `json:"bid"`
	Bidder     uint8  `json:"bidder"`
	BidderGov  uint8  `json:"bidder_gov"`
	StarFrom   uint8  `json:"star_from"`
	PlanetFrom uint8  `json:"planet_from"`
	StarTo     uint8  `json:"star_to"`
	PlanetTo   uint8  `json:"planet_to"`
}
