package model

// Toggle holds per-governor display/behavior toggles.
// Corresponds to toggletype in races.h.
type Toggle struct {
	Invisible     bool `json:"invisible"`
	Standby       bool `json:"standby"`
	Color         bool `json:"color"`
	Gag           bool `json:"gag"`
	DoubleDigits  bool `json:"double_digits"`
	Inverse       bool `json:"inverse"`
	Geography     bool `json:"geography"`
	AutoLoad      bool `json:"auto_load"`
	Highlight     uint8 `json:"highlight"` // race number to highlight
	Compat        bool `json:"compat"`
}

// Governor holds per-governor state within a race.
// Corresponds to struct gov inside struct race in races.h.
type Governor struct {
	Name           string  `json:"name"`
	Password       string  `json:"password"`
	Active         bool    `json:"active"`
	DefLevel       uint8   `json:"def_level"`
	DefSystem      uint8   `json:"def_system"`
	DefPlanetNum   uint8   `json:"def_planet_num"`
	HomeLevel      uint8   `json:"home_level"`
	HomeSystem     uint8   `json:"home_system"`
	HomePlanetNum  uint8   `json:"home_planet_num"`
	NewsPos        [4]uint64 `json:"news_pos"` // file positions in news streams
	Toggle         Toggle  `json:"toggle"`
	Money          uint64  `json:"money"`
	Income         uint64  `json:"income"`
	Maintain       uint64  `json:"maintain"`
	CostTech       uint64  `json:"cost_tech"`
	CostMarket     uint64  `json:"cost_market"`
	ProfitMarket   uint64  `json:"profit_market"`
	Login          int64   `json:"login"` // Unix timestamp of last login
}

// Race represents one player race.
// Corresponds to struct race / racetype in races.h.
//
// AtWar and Allied are 64-bit bitmaps: bit N set means player N+1.
// The C code uses [2]uint32; Go uses uint64 directly.
type Race struct {
	PlayerNum     int    `json:"player_num"`
	Name          string `json:"name"`
	Password      string `json:"password"` // hashed in practice
	Info          string `json:"info"`
	Motto         string `json:"motto"`

	// Racial traits
	Absorb       bool    `json:"absorb"`
	CollectiveIQ bool    `json:"collective_iq"`
	Pods         bool    `json:"pods"`
	Fighters     uint8   `json:"fighters"`
	IQ           uint8   `json:"iq"`
	IQLimit      uint8   `json:"iq_limit"`
	NumberSexes  uint8   `json:"number_sexes"`
	Fertilize    uint8   `json:"fertilize"`
	Adventurism  float64 `json:"adventurism"`
	BirthRate    float64 `json:"birth_rate"`
	Mass         float64 `json:"mass"`
	Metabolism   float64 `json:"metabolism"`

	// Environmental preferences
	// Indexed by AtmosXxx constants; length = AtmosToxic+1 = 11
	Conditions [AtmosToxic + 1]int16   `json:"conditions"`
	// Sector type compatibility; indexed by SectorXxx constants
	// length = SectorWasted+1 = 9
	Likes      [SectorWasted + 1]float64 `json:"likes"`
	LikesBest  uint8                     `json:"likes_best"`

	// Status flags
	Dissolved  bool `json:"dissolved"`
	God        bool `json:"god"`
	Guest      bool `json:"guest"`
	Metamorph  bool `json:"metamorph"`
	Monitor    bool `json:"monitor"`

	// Diplomacy — 64-bit bitmaps (bit N = player N+1)
	AtWar  uint64 `json:"at_war"`
	Allied uint64 `json:"allied"`

	// Translation modifier per player
	Translate [MaxPlayers]int8 `json:"translate"`

	GovShip           uint32             `json:"gov_ship"` // ship number of government ship
	Morale            int64              `json:"morale"`
	Points            [MaxPlayers]uint32 `json:"points"`
	ControlledPlanets uint16             `json:"controlled_planets"`
	VictoryTurns      uint16             `json:"victory_turns"`
	Turn              uint16             `json:"turn"`

	Tech         float64  `json:"tech"`
	Discoveries  [80]uint8 `json:"discoveries"` // indexed by DiscXxx constants
	VictoryScore uint64   `json:"victory_score"`
	Votes        uint64   `json:"votes"`
	PlanetPoints uint64   `json:"planet_points"`

	Governors int8                          `json:"governors"` // number of governors
	Governor  [MaxGovernors + 1]Governor    `json:"governor"`
}

// IsAtWar returns true if this race is at war with the given player.
func (r *Race) IsAtWar(playerNum int) bool {
	return r.AtWar&(1<<uint(playerNum-1)) != 0
}

// IsAllied returns true if this race is allied with the given player.
func (r *Race) IsAllied(playerNum int) bool {
	return r.Allied&(1<<uint(playerNum-1)) != 0
}

// HasDiscovery returns true if the race has unlocked a given technology.
func (r *Race) HasDiscovery(d int) bool {
	return d < len(r.Discoveries) && r.Discoveries[d] != 0
}

// Block represents a power block (alliance group).
// Corresponds to struct block / blocktype in races.h.
type Block struct {
	PlayerNum    int    `json:"player_num"`
	Name         string `json:"name"`
	Motto        string `json:"motto"`
	Invite       uint64 `json:"invite"`
	Pledge       uint64 `json:"pledge"`
	AtWar        uint64 `json:"at_war"`
	Allied       uint64 `json:"allied"`
	Next         uint16 `json:"next"`
	SystemsOwned uint16 `json:"systems_owned"`
	VPs          uint64 `json:"vps"`
	Money        uint64 `json:"money"`
}
