package model

// NavigateState holds navigation mode parameters.
// Corresponds to the navigate bitfield struct in ships.h.
type NavigateState struct {
	On      bool   `json:"on"`
	Speed   uint8  `json:"speed"`   // 0–9 (4 bits in C)
	Turns   uint16 `json:"turns"`   // turns remaining (15 bits in C)
	Bearing uint16 `json:"bearing"` // course in degrees (9 bits in C)
}

// ProtectState holds autoshoot/defense configuration.
// Corresponds to the protect bitfield struct in ships.h.
type ProtectState struct {
	MaxRange float64 `json:"max_range"`
	On       bool    `json:"on"`
	Planet   bool    `json:"planet"` // defending a planet
	Self     bool    `json:"self"`   // retaliate if attacked
	Evade    bool    `json:"evade"`  // evasive action
	Ship     uint16  `json:"ship"`   // ship number being protected (14 bits in C)
}

// HyperDriveState holds FTL drive state.
type HyperDriveState struct {
	Charge uint8 `json:"charge"`
	Ready  bool  `json:"ready"`
	On     bool  `json:"on"`
	Has    bool  `json:"has"`
}

// --- Special union fields ---
// Only one will be non-nil for a given ship type.

// AimedAtSpecial is used by space mirrors (StypeMirror).
type AimedAtSpecial struct {
	ShipNo    uint16 `json:"ship_no"`
	StarNum   uint8  `json:"star_num"`
	Intensity int8   `json:"intensity"`
	PlanetNum uint8  `json:"planet_num"`
	Level     uint8  `json:"level"`
}

// MindSpecial is used by VNs (OtypeVN) and Berserkers (OtypeBers).
type MindSpecial struct {
	Progenitor uint8 `json:"progenitor"`
	Target     uint8 `json:"target"`
	Generation uint8 `json:"generation"`
	Busy       bool  `json:"busy"`
	Tampered   bool  `json:"tampered"`
	WhoKilled  uint8 `json:"who_killed"`
}

// PodSpecial is used by spore pods (StypePod).
type PodSpecial struct {
	Decay       uint8 `json:"decay"`
	Temperature uint8 `json:"temperature"`
}

// TimerSpecial is used by dust canisters (OtypeCanist) and greenhouse gas (OtypeGreen).
type TimerSpecial struct {
	Count uint8 `json:"count"`
}

// ImpactSpecial is used by missiles (StypeMissile).
type ImpactSpecial struct {
	X       uint8 `json:"x"`
	Y       uint8 `json:"y"`
	Scatter uint8 `json:"scatter"`
}

// TriggerSpecial is used by mines (StypeMine).
type TriggerSpecial struct {
	Radius uint16 `json:"radius"`
}

// TerraformSpecial is used by terraformers (OtypeTerra).
type TerraformSpecial struct {
	Index uint8 `json:"index"`
}

// TransportSpecial is used by AVPM transporters (OtypeTransDev).
type TransportSpecial struct {
	Target uint16 `json:"target"`
}

// WasteSpecial is used by toxic waste containers (OtypeToxWC).
type WasteSpecial struct {
	Toxic uint8 `json:"toxic"`
}

// ShipSpecial holds type-specific data. Exactly one pointer will be
// non-nil, corresponding to the ship's type. Mirrors the C union special.
type ShipSpecial struct {
	AimedAt   *AimedAtSpecial   `json:"aimed_at,omitempty"`
	Mind      *MindSpecial      `json:"mind,omitempty"`
	Pod       *PodSpecial       `json:"pod,omitempty"`
	Timer     *TimerSpecial     `json:"timer,omitempty"`
	Impact    *ImpactSpecial    `json:"impact,omitempty"`
	Trigger   *TriggerSpecial   `json:"trigger,omitempty"`
	Terraform *TerraformSpecial `json:"terraform,omitempty"`
	Transport *TransportSpecial `json:"transport,omitempty"`
	Waste     *WasteSpecial     `json:"waste,omitempty"`
}

// Ship represents one ship or object in the game.
// This is the Go equivalent of struct ship / shiptype in ships.h.
//
// Linked-list fields (NextShip, Ships, DestShipNo) are preserved for
// compatibility with the C turn logic; they will be rebuilt each turn
// from the DB's orbit index.
type Ship struct {
	Number    uint32   `json:"number"`
	Owner     uint8    `json:"owner"`
	Governor  uint8    `json:"governor"`
	Name      string   `json:"name"`
	Class     string   `json:"class"`
	Race      uint8    `json:"race"`       // usually == Owner; differs after revolt
	Type      ShipType `json:"type"`
	WhoKilled int16    `json:"who_killed"` // player who killed this ship

	// Position
	XPos float64 `json:"x_pos"`
	YPos float64 `json:"y_pos"`
	LandX uint8  `json:"land_x"`
	LandY uint8  `json:"land_y"`

	// Orbit / destination
	StOrbits   uint8  `json:"st_orbits"`    // star number being orbited
	PNumOrbits uint8  `json:"pnum_orbits"`  // planet number if orbiting
	WhatOrbits uint8  `json:"what_orbits"`  // scope level of orbit
	DestStar   uint8  `json:"dest_star"`
	DestPNum   uint8  `json:"dest_pnum"`
	WhatDest   uint8  `json:"what_dest"`
	DestShipNo uint32 `json:"dest_ship_no"`

	// Linked-list orbit chains (rebuilt each turn; stored for serialization)
	NextShip uint32 `json:"next_ship"` // next ship in same orbit
	Ships    uint32 `json:"ships"`     // first ship docked/landed here

	// Physical stats
	Fuel      float64 `json:"fuel"`
	Mass      float64 `json:"mass"`
	BaseMass  float64 `json:"base_mass"`
	Tech      float64 `json:"tech"`
	Complexity float64 `json:"complexity"`
	Armor     uint8   `json:"armor"`
	Size      uint16  `json:"size"`
	Damage    uint8   `json:"damage"`
	Rad       uint8   `json:"rad"`
	Speed     uint8   `json:"speed"` // current travel speed 0–9

	// Capacities
	MaxCrew     uint16 `json:"max_crew"`
	MaxResource uint16 `json:"max_resource"`
	MaxDestruct uint16 `json:"max_destruct"`
	MaxFuel     uint16 `json:"max_fuel"`
	MaxSpeed    uint16 `json:"max_speed"`
	MaxHanger   uint16 `json:"max_hanger"`
	Hanger      uint16 `json:"hanger"` // hanger space used

	// Cargo
	Destruct  uint16 `json:"destruct"`
	Resource  uint16 `json:"resource"`
	Popn      uint16 `json:"popn"`   // crew
	Troops    uint16 `json:"troops"` // marines
	Crystals  uint16 `json:"crystals"`

	// Weapons
	Guns      uint8 `json:"guns"`      // active gun system (GunNone/Primary/Secondary)
	Primary   uint8 `json:"primary"`   // primary gun strength
	PrimType  uint8 `json:"prim_type"`
	Secondary uint8 `json:"secondary"` // secondary gun strength
	SecType   uint8 `json:"sec_type"`
	Retaliate uint8 `json:"retaliate"`
	Target    uint16 `json:"target"`

	// Build factory fields
	BuildType uint16 `json:"build_type"`
	BuildCost uint16 `json:"build_cost"`

	// Special systems
	Mount      uint8           `json:"mount"`       // has crystal mount
	HyperDrive HyperDriveState `json:"hyper_drive"`
	CEW        uint8           `json:"cew"`         // CEW strength
	CEWRange   uint16          `json:"cew_range"`
	Cloak      uint8           `json:"cloak"`
	Laser      uint8           `json:"laser"`
	Focus      uint8           `json:"focus"`
	FireLaser  uint8           `json:"fire_laser"`

	// State flags
	Active   bool `json:"active"`
	Alive    bool `json:"alive"`
	Mode     bool `json:"mode"`
	Bombard  bool `json:"bombard"`
	Mounted  bool `json:"mounted"`  // crystal currently mounted
	Cloaked  bool `json:"cloaked"`
	Sheep    bool `json:"sheep"`    // under mind control
	Docked   bool `json:"docked"`
	Notified bool `json:"notified"`
	Examined bool `json:"examined"`
	On       bool `json:"on"`

	// Merchant shipping
	Merchant uint8 `json:"merchant"` // route number

	// Navigation and defense modes
	Navigate NavigateState `json:"navigate"`
	Protect  ProtectState  `json:"protect"`

	// Type-specific data (only one field non-nil)
	Special ShipSpecial `json:"special"`
}

// --- Computed accessors (mirrors of C macros) ---

// Armor returns effective armor accounting for damage.
func (s *Ship) EffArmor(data ShipdataTable) int64 {
	if s.Type == OtypeFactory {
		return data[s.Type][AbilArmor]
	}
	return int64(s.Armor) * int64(100-s.Damage) / 100
}

// Guns returns the current gun strength.
func (s *Ship) GunStrength() uint8 {
	switch s.Guns {
	case GunNone:
		return 0
	case GunPrimary:
		return s.Primary
	default:
		return s.Secondary
	}
}

// CanBombard returns true if the ship can bombard planets.
func (s *Ship) CanBombard(data ShipdataTable) bool {
	return data[s.Type][AbilGuns] > 0 && s.Type != StypeMine
}

// CanNavigate returns true if the ship can move under its own power.
func (s *Ship) CanNavigate(data ShipdataTable) bool {
	return data[s.Type][AbilSpeed] > 0 && s.Type != OtypeTerra && s.Type != OtypeVN
}

// Sight returns true if the ship can observe its surroundings.
func (s *Ship) Sight() bool {
	return s.Type == OtypeProbe || s.Popn > 0
}

// Place represents a parsed location in the universe hierarchy.
// Used as the return type for getplace() / cs.c.
type Place struct {
	StarNum  uint8
	PlanetNum uint8
	ShipNo   uint32
	Level    uint8
	Err      bool
}
