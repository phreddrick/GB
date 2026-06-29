package model

// Scope levels (Dir.Level / ship.WhatDest / ship.WhatOrbits)
const (
	LevelUniv = 0
	LevelStar = 1
	LevelPlan = 2
	LevelShip = 3
)

const (
	MaxPlayers   = 64
	MaxGovernors = 5
	NumStars     = 256
	MaxPlanets   = 10
	MaxRoutes    = 4
	MaxX         = 45
	MaxY         = 19
)

// Physics constants
const (
	// SystemGravConst is used in Kepler's third law orbital period calculation.
	// Mirrors SYSTEMGRAVCONST in tweakables.h.
	SystemGravConst = 150000.0
)

// ShipType identifies the type of a ship or object.
type ShipType uint8

const (
	StypePod      ShipType = 0
	StypeShuttle  ShipType = 1
	StypeCarrier  ShipType = 2
	StypeDreadnt  ShipType = 3
	StypeBattle   ShipType = 4
	StypeIntcpt   ShipType = 5
	StypeCruiser  ShipType = 6
	StypeDestroyr ShipType = 7
	StypeFighter  ShipType = 8
	StypeExplorer ShipType = 9
	StypeHabitat  ShipType = 10
	StypeStation  ShipType = 11
	StypeOAP      ShipType = 12
	StypeCargo    ShipType = 13
	StypeTanker   ShipType = 14
	StypeGod      ShipType = 15
	StypeMine     ShipType = 16
	StypeMirror   ShipType = 17
	OtypeStele    ShipType = 18
	OtypeGtele    ShipType = 19
	OtypeTract    ShipType = 20
	OtypeAP       ShipType = 21
	OtypeCanist   ShipType = 22
	OtypeGreen    ShipType = 23
	OtypeVN       ShipType = 24
	OtypeBers     ShipType = 25
	OtypeGov      ShipType = 26
	OtypeOMCL     ShipType = 27
	OtypeToxWC    ShipType = 28
	OtypeProbe    ShipType = 29
	OtypeGR       ShipType = 30
	OtypeFactory  ShipType = 31
	OtypeTerra    ShipType = 32
	OtypeBersCLC  ShipType = 33
	OtypeAutoFac  ShipType = 34
	OtypeTransDev ShipType = 35
	StypeMissile  ShipType = 36
	OtypePlanDef  ShipType = 37
	OtypeQuarry   ShipType = 38
	OtypePlow     ShipType = 39
	OtypeDome     ShipType = 40
	OtypeWPlant   ShipType = 41
	OtypePort     ShipType = 42
	OtypeABM      ShipType = 43
	OtypeAFV      ShipType = 44
	OtypeBunker   ShipType = 45
	StypeLander   ShipType = 46

	NumShipTypes = 47
)

// Ability indices into Shipdata table
type Ability int

const (
	AbilTech      Ability = 0
	AbilCargo     Ability = 1
	AbilHanger    Ability = 2
	AbilDestCap   Ability = 3
	AbilGuns      Ability = 4
	AbilPrimary   Ability = 5
	AbilSecondary Ability = 6
	AbilFuelCap   Ability = 7
	AbilMaxCrew   Ability = 8
	AbilArmor     Ability = 9
	AbilCost      Ability = 10
	AbilMount     Ability = 11
	AbilJump      Ability = 12
	AbilCanLand   Ability = 13
	AbilHasSwitch Ability = 14
	AbilSpeed     Ability = 15
	AbilDamage    Ability = 16
	AbilBuild     Ability = 17
	AbilConstruct Ability = 18
	AbilMod       Ability = 19
	AbilLaser     Ability = 20
	AbilCEW       Ability = 21
	AbilCloak     Ability = 22
	AbilGod       Ability = 23
	AbilProgrammd Ability = 24
	AbilPort      Ability = 25
	AbilRepair    Ability = 26
	AbilMaintain  Ability = 27

	NumAbils = 28
)

// Gun modes
const (
	GunNone      = 0
	GunPrimary   = 1
	GunSecondary = 2
)

// Sector types
const (
	SectorSea    = 0
	SectorLand   = 1
	SectorMount  = 2
	SectorGas    = 3
	SectorIce    = 4
	SectorForest = 5
	SectorDesert = 6
	SectorPlated = 7
	SectorWasted = 8
)

// Planet types
const (
	PlanetEarth    = 0
	PlanetAsteroid = 1
	PlanetMars     = 2
	PlanetIceball  = 3
	PlanetGasGiant = 4
	PlanetWater    = 5
	PlanetForest   = 6
	PlanetDesert   = 7
)

// Atmosphere condition indices (used in conditions[] and likes[])
const (
	AtmosRTemp    = 0
	AtmosTemp     = 1
	AtmosMethane  = 2
	AtmosOxygen   = 3
	AtmosCO2      = 4
	AtmosHydrogen = 5
	AtmosNitrogen = 6
	AtmosSulfur   = 7
	AtmosHelium   = 8
	AtmosOther    = 9
	AtmosToxic    = 10
)

// News / telegram types
const (
	NewsDeclaration = 0
	NewsTransfer    = 1
	NewsCombat      = 2
	NewsAnnounce    = 3
)

// Commodity types (for market)
const (
	CommodResource = 0
	CommodDestruct = 1
	CommodFuel     = 2
	CommodCrystal  = 3
)

// Commodity load/unload bitmasks
const (
	MFuel      = 0x1
	MDestruct  = 0x2
	MResources = 0x4
	MCrystals  = 0x8
)

// Technology discovery indices
const (
	DiscHyperDrive  = 0
	DiscLaser       = 1
	DiscCEW         = 2
	DiscVN          = 3
	DiscTractorBeam = 4
	DiscTransporter = 5
	DiscAVPM        = 6
	DiscCloak       = 7
	DiscWormhole    = 8
	DiscCrystal     = 9
)

// Tech thresholds for discoveries
const (
	TechHyperDrive  = 50.0
	TechLaser       = 100.0
	TechCEW         = 150.0
	TechVN          = 100.0
	TechTractorBeam = 999.0
	TechTransporter = 999.0
	TechAVPM        = 250.0
	TechCloak       = 999.0
	TechWormhole    = 999.0
	TechCrystal     = 50.0
)
