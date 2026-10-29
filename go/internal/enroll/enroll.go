// Package enroll handles race enrollment via web interface.
// This extracts and adapts the logic from cmd/racegen/main.go for use in the web server.
package enroll

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/model"
)

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

// Enroll validates a request and returns a model.Race ready for storage.
// Also returns any validation errors.
func Enroll(req *Request, playerNum int) (*model.Race, []string, error) {
	var errs []string

	// Validate required fields
	if req.RaceName == "" {
		errs = append(errs, "race name is required")
	}
	if req.Password == "" {
		errs = append(errs, "password is required")
	}
	if len(req.Password) < 3 {
		errs = append(errs, "password must be at least 3 characters")
	}

	// Validate IQ
	if req.IQ < 1 || req.IQ > 100 {
		errs = append(errs, "IQ must be between 1 and 100")
	}

	// Validate Fertilize
	if req.Fertilize < 1 || req.Fertilize > 100 {
		errs = append(errs, "fertilize must be between 1 and 100")
	}

	// Validate BirthRate
	if req.BirthRate < 0.25 || req.BirthRate > 1.0 {
		errs = append(errs, "birth rate must be between 0.25 and 1.0")
	}

	// Validate Mass
	if req.Mass < 0.1 || req.Mass > 10.0 {
		errs = append(errs, "mass must be between 0.1 and 10.0")
	}

	// Validate Metabolism
	if req.Metabolism < 0.1 || req.Metabolism > 10.0 {
		errs = append(errs, "metabolism must be between 0.1 and 10.0")
	}

	// Validate NumberSexes
	if req.NumberSexes < 1 || req.NumberSexes > 15 {
		errs = append(errs, "number of sexes must be between 1 and 15")
	}

	// Validate Adventurism
	if req.Adventurism < 0.0 || req.Adventurism > 100.0 {
		errs = append(errs, "adventurism must be between 0 and 100")
	}

	// Validate HomePlanetType
	if req.HomePlanetType < 0 || req.HomePlanetType > 6 {
		errs = append(errs, "invalid home planet type")
	}

	if len(errs) > 0 {
		return nil, errs, nil
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		// Fallback: store plain-text (should not happen in production)
		hash = []byte(req.Password)
	}
	hashedPassword := string(hash)

	// Map home planet type C values to Go model constants
	homePlanetMap := [7]uint8{
		model.PlanetEarth,
		model.PlanetForest,
		model.PlanetDesert,
		model.PlanetWater,
		model.PlanetAsteroid,
		model.PlanetIceball,
		model.PlanetGasGiant,
	}

	race := &model.Race{
		PlayerNum:   playerNum,
		Name:        req.RaceName,
		Password:    hashedPassword,
		Info:        req.Info,
		IQ:          req.IQ,
		IQLimit:     req.IQ,
		Fertilize:   req.Fertilize,
		BirthRate:   req.BirthRate,
		Mass:        req.Mass,
		Metabolism:  req.Metabolism,
		NumberSexes: req.NumberSexes,
		Adventurism: req.Adventurism,
		LikesBest:   homePlanetMap[req.HomePlanetType],
	}

	// Set sector compatibilities (normalize 0–100 to 0–1)
	for i := 0; i < 8 && i < len(race.Likes); i++ {
		race.Likes[i] = req.SectorCompat[i] / 100.0
	}

	// Set environmental conditions (neutral default)
	for i := 0; i <= model.AtmosToxic; i++ {
		race.Conditions[i] = 50
	}

	// Set governor 0 as active
	race.Governors = 1
	race.Governor[0] = model.Governor{
		Name:     req.RaceName,
		Password: hashedPassword,
		Active:   true,
	}

	return race, nil, nil
}
