package model

// Star represents one star system.
// Corresponds to struct star / startype in vars.h.
//
// The C code uses explored[2] / inhabited[2] as two uint32s forming a
// 64-bit bitmap (one bit per player). Go uses uint64 directly.
type Star struct {
	Number     uint8    `json:"number"`
	Name       string   `json:"name"`
	XPos       float64  `json:"x_pos"`
	YPos       float64  `json:"y_pos"`
	Gravity    float64  `json:"gravity"`
	Temperature uint8   `json:"temperature"`
	Stability  uint8    `json:"stability"`
	NovaStage  uint8    `json:"nova_stage"`
	NumPlanets uint8    `json:"num_planets"`
	PlanetNames [MaxPlanets]string `json:"planet_names"`

	// First ship orbiting this star
	Ships uint32 `json:"ships"`

	// Per-player data
	Governor [MaxPlayers]uint8 `json:"governor"` // which governor manages this system
	AP       [MaxPlayers]uint8 `json:"ap"`        // action points available

	// 64-bit bitmaps: bit N set means player N+1 has explored / inhabits this star
	Explored  uint64 `json:"explored"`
	Inhabited uint64 `json:"inhabited"`
}

// IsExplored returns true if the given player (1-indexed) has explored this star.
func (s *Star) IsExplored(playerNum int) bool {
	return s.Explored&(1<<uint(playerNum-1)) != 0
}

// SetExplored marks the star as explored by the given player.
func (s *Star) SetExplored(playerNum int) {
	s.Explored |= 1 << uint(playerNum-1)
}

// IsInhabited returns true if the given player currently inhabits this star.
func (s *Star) IsInhabited(playerNum int) bool {
	return s.Inhabited&(1<<uint(playerNum-1)) != 0
}

// SetInhabited marks the star as inhabited by the given player.
func (s *Star) SetInhabited(playerNum int) {
	s.Inhabited |= 1 << uint(playerNum-1)
}

// ClearInhabited clears the inhabited bit for the given player.
func (s *Star) ClearInhabited(playerNum int) {
	s.Inhabited &^= 1 << uint(playerNum-1)
}

// StarData holds universe-level global state.
// Corresponds to struct stardata in vars.h.
type StarData struct {
	NumStars uint16 `json:"num_stars"`
	Ships    uint32 `json:"ships"` // first ship in universe orbit

	// Per-player totals for VN tracking
	AP            [MaxPlayers]uint8  `json:"ap"`
	VNHitList     [MaxPlayers]uint16 `json:"vn_hit_list"`
	VNIndex1      [MaxPlayers]uint8  `json:"vn_index1"`
	VNIndex2      [MaxPlayers]uint8  `json:"vn_index2"`
}

// Directory holds a governor's current scope (where they are looking).
// Corresponds to struct directory in vars.h, indexed Dir[playernum-1][governor].
type Directory struct {
	Level     uint8   `json:"level"`  // LevelUniv, LevelStar, LevelPlan, LevelShip
	StarNum   uint8   `json:"star_num"`
	PlanetNum uint8   `json:"planet_num"`
	ShipNo    uint32  `json:"ship_no"`
	Prompt    string  `json:"prompt"`
	LastX     [2]float64 `json:"last_x"`
	LastY     [2]float64 `json:"last_y"`
	Zoom      [2]float64 `json:"zoom"`
}

// VicScore holds victory score data for one race.
// Corresponds to struct vic in vars.h.
type VicScore struct {
	RaceNum  uint8   `json:"race_num"`
	Name     string  `json:"name"`
	NoCount  uint64  `json:"no_count"`
	Sleep    bool    `json:"sleep"`
	Tech     float64 `json:"tech"`
	Thing    int     `json:"thing"`
	IQ       int     `json:"iq"`
	RawScore int64   `json:"raw_score"`
	Login    int64   `json:"login"`
}
