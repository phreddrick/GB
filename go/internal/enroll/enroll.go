// Package enroll retains legacy disabled HTTP request/response types only.
// CR-07D retired the incomplete race builder and its plaintext fallback.
package enroll

// Request represents an enrollment request from the web form.
type Request struct {
	RaceName       string  `json:"race_name"`
	Password       string  `json:"password"`
	Info           string  `json:"info"`
	HomePlanetType int     `json:"home_planet_type"` // 0=Earth, 1=Forest, etc.
	IQ             uint8   `json:"iq"`
	Fertilize      uint8   `json:"fertilize"`
	BirthRate      float64 `json:"birth_rate"`
	Mass           float64 `json:"mass"`
	Metabolism     float64 `json:"metabolism"`
	NumberSexes    uint8   `json:"number_sexes"`
	Adventurism    float64 `json:"adventurism"`
	// Sector compatibilities (0–100, will be normalized to 0–1)
	SectorCompat [8]float64 `json:"sector_compat"`
}

// Response represents the result of an enrollment attempt.
type Response struct {
	Success   bool     `json:"success"`
	PlayerNum int      `json:"player_num,omitempty"`
	Message   string   `json:"message"`
	Errors    []string `json:"errors,omitempty"`
}
