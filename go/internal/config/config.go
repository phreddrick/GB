// Package config holds the server's runtime configuration: the tunable
// constants that were scattered across config.h and tweakables.h in the C
// source, plus the operational settings (addresses, database path, secrets)
// that Phase 1 reads from the environment. Precedence and validation are
// documented in env.go and validate.go.
package config

// Config holds all server and game-balance configuration.
// A zero-value Config is not valid; use Default() to obtain sane defaults.
type Config struct {
	// --- Server networking ---
	Host    string `json:"host"`
	Port    int    `json:"port"`     // HTTP/WebSocket port
	TLSCert string `json:"tls_cert"` // path to TLS cert file (empty = no TLS)
	TLSKey  string `json:"tls_key"`

	// --- Administrative listener (Phase 1 transitional interface) ---
	// AdminAddr is the host:port of the token-protected admin listener
	// (gbctl talks to it). Loopback by default; empty disables the listener
	// (and with it update triggers and universe initialisation).
	AdminAddr string `json:"admin_addr"`
	// AdminToken is never read from the JSON file; it comes from GB_ADMIN_TOKEN
	// or GB_ADMIN_TOKEN_FILE only (see env.go). The json:"-" tag enforces it.
	AdminToken string `json:"-"`

	// --- JWT authentication ---
	// JWTSecret is never read from the JSON file; it comes from GB_JWT_SECRET
	// or GB_JWT_SECRET_FILE only (see env.go). There is no default: the server
	// refuses to start without a secret that passes validate.go (CR-35).
	JWTSecret string `json:"-"`

	// --- Database ---
	DBPath string `json:"db_path"` // path to bbolt database file

	// --- Operations ---
	LogLevel               string   `json:"log_level"`                  // debug, info, warn, error
	ShutdownTimeoutSeconds int      `json:"shutdown_timeout_seconds"`   // orderly shutdown budget
	TurnStallTimeoutSecs   int      `json:"turn_stall_timeout_seconds"` // liveness: max time without turn progress
	WSAllowedOrigins       []string `json:"ws_allowed_origins"`         // browser Origin allow-list; empty = same-origin only
	WSMaxMessageBytes      int64    `json:"ws_max_message_bytes"`       // read limit per WebSocket frame
	WSWriteTimeoutSeconds  int      `json:"ws_write_timeout_seconds"`   // per-write deadline
	WSOutboundQueue        int      `json:"ws_outbound_queue"`          // queued messages before a slow client is dropped
	EnrollPreview          bool     `json:"enroll_preview"`             // enable the INCOMPLETE web enrollment preview (CR-07)

	// --- Data directories ---
	DataDir string `json:"data_dir"` // game data root
	DocsDir string `json:"docs_dir"` // help / docs files
	TeleDir string `json:"tele_dir"` // telegram directory
	NewsDir string `json:"news_dir"` // news files

	// --- Command rate limiting ---
	CommandTimeMSec  int `json:"command_time_msec"`  // time slice length in ms
	CommandsPerTime  int `json:"commands_per_time"`  // commands per time slice after burst
	CommandBurstSize int `json:"command_burst_size"` // commands allowed per burst
	DisconnectTime   int `json:"disconnect_time"`    // max idle seconds before disconnect

	// --- Timing ---
	DefaultUpdateMinutes      int `json:"default_update_minutes"`      // minutes between full updates
	DefaultRandomUpdateRange  int `json:"default_random_update_range"` // random variation in minutes
	DefaultRandomSegmentRange int `json:"default_random_segment_range"`
	MovesPerUpdate            int `json:"moves_per_update"` // movement segments per update

	// --- Feature flags (replaces #define in config.h) ---
	Market       bool `json:"market"`        // commodity market system
	Victory      bool `json:"victory"`       // victory conditions
	Defense      bool `json:"defense"`       // planetary gun emplacements
	Voting       bool `json:"voting"`        // player voting
	Dissolve     bool `json:"dissolve"`      // allow races to dissolve
	Monitor      bool `json:"monitor"`       // deity can monitor messages
	AccessCheck  bool `json:"access_check"`  // address authorization
	Nomads       bool `json:"nomads"`        // nomad race rules
	PodTerraform bool `json:"pod_terraform"` // pods terraform on infection
	ShowCowards  bool `json:"show_cowards"`  // show invisible player count

	// --- Game balance (from tweakables.h) ---
	MoveFactor         float64 `json:"move_factor"`
	LimitAPs           int     `json:"limit_aps"` // max APs a player can have
	MaxCommStrSize     int     `json:"max_comm_str_size"`
	CommandSize        int     `json:"command_size"`
	MaxArgs            int     `json:"max_args"`
	LoginNameSize      int     `json:"login_name_size"`
	NumTimesToWaitLock int     `json:"num_times_to_wait_lock"`

	// Map / planet dimensions
	UnivSize    float64 `json:"univ_size"`
	SystemSize  float64 `json:"system_size"`
	PLOrbitSize float64 `json:"pl_orbit_size"`
	MaxSectPopn int     `json:"max_sect_popn"`
	ToxMax      int     `json:"tox_max"`

	// Ship physics — mass contributions
	MassFuel     float64 `json:"mass_fuel"`
	MassResource float64 `json:"mass_resource"`
	MassDestruct float64 `json:"mass_destruct"`
	MassArmor    float64 `json:"mass_armor"`
	MassSize     float64 `json:"mass_size"`
	MassHanger   float64 `json:"mass_hanger"`
	MassGuns     float64 `json:"mass_guns"`

	// Ship build sizing — SIZE_* constants from tweakables.h
	SizeGuns     float64 `json:"size_guns"`
	SizeCrew     float64 `json:"size_crew"`
	SizeResource float64 `json:"size_resource"`
	SizeFuel     float64 `json:"size_fuel"`
	SizeDestruct float64 `json:"size_destruct"`

	// Economy & production
	ResourceProduction float64 `json:"resource_production"`
	FuelProduction     float64 `json:"fuel_production"`
	DestProduction     float64 `json:"dest_production"`
	PopnProd           float64 `json:"popn_prod"`
	EffProd            float64 `json:"eff_prod"`
	FactorFertSupport  float64 `json:"factor_fert_support"`
	HabitatProdRate    float64 `json:"habitat_prod_rate"`
	HabitatPopRate     float64 `json:"habitat_pop_rate"`

	// Combat
	SectorDamage     float64 `json:"sector_damage"`
	ShipDamage       float64 `json:"ship_damage"`
	FactorDamage     float64 `json:"factor_damage"`
	FactorDestPlanet float64 `json:"factor_dest_planet"`
	PlanFireLim      int     `json:"plan_fire_lim"`
	MechAttack       float64 `json:"mech_attack"`

	// Navigation / movement
	FuelUse               float64 `json:"fuel_use"`
	HyperDriveFuelUse     float64 `json:"hyper_drive_fuel_use"`
	HyperDistFactor       float64 `json:"hyper_dist_factor"`
	HyperDriveReadyCharge int     `json:"hyper_drive_ready_charge"`
	LaunchGravMassFactor  float64 `json:"launch_grav_mass_factor"`
	LandGravMassFactor    float64 `json:"land_grav_mass_factor"`
	FuelGasAdd            float64 `json:"fuel_gas_add"`
	FuelGasAddTanker      float64 `json:"fuel_gas_add_tanker"`
	FuelGasAddHabitat     float64 `json:"fuel_gas_add_habitat"`
	FuelGasAddStation     float64 `json:"fuel_gas_add_station"`
	DistToLand            float64 `json:"dist_to_land"`
	DistToDock            float64 `json:"dist_to_dock"`
	StrikeDistanceFactor  float64 `json:"strike_distance_factor"`
	ComplexityFactor      float64 `json:"complexity_factor"`
	GravFactor            float64 `json:"grav_factor"`
	PlanetGravConst       float64 `json:"planet_grav_const"`
	SystemGravConst       float64 `json:"system_grav_const"`

	// Tech
	TechInvest       float64 `json:"tech_invest"`
	TechScale        float64 `json:"tech_scale"`
	TechSeeStability int     `json:"tech_see_stability"`
	TechExplore      int     `json:"tech_explore"`

	// Population movement
	PopnMoveScale1 int     `json:"popn_move_scale_1"`
	PopnMoveScale2 int     `json:"popn_move_scale_2"`
	ShipMoveScale  float64 `json:"ship_move_scale"`

	// Terraforming / environment
	FuelCostTerra     float64 `json:"fuel_cost_terra"`
	FuelCostQuarry    float64 `json:"fuel_cost_quarry"`
	FuelCostPlow      float64 `json:"fuel_cost_plow"`
	ResCostDome       float64 `json:"res_cost_dome"`
	ResCostWPlant     float64 `json:"res_cost_wplant"`
	FuelCostWPlant    float64 `json:"fuel_cost_wplant"`
	EnvirDamageTox    int     `json:"envir_damage_tox"`
	ResourceDepletion float64 `json:"resource_depletion"`

	// Troops / mobilization
	EnlistTroopCost int     `json:"enlist_troop_cost"`
	UpdateTroopCost int     `json:"update_troop_cost"`
	MobCost         float64 `json:"mob_cost"`
	FactorMobProd   float64 `json:"factor_mob_prod"`
	FuelManeuver    float64 `json:"fuel_maneuver"`

	// Repair
	RepairRate       float64 `json:"repair_rate"`
	SectorRepairCost int     `json:"sector_repair_cost"`
	NaturalRepair    int     `json:"natural_repair"`

	// Misc
	VNResTake        float64 `json:"vn_res_take"`
	APFactor         float64 `json:"ap_factor"`
	Dissipate        int     `json:"dissipate"`
	PodThreshold     int     `json:"pod_threshold"`
	PodDecay         int     `json:"pod_decay"`
	SporeSuccessRate int     `json:"spore_success_rate"`
	MaxGovernors     int     `json:"max_governors"`
	MesoPopScale     float64 `json:"meso_pop_scale"`
	IncomeFactor     float64 `json:"income_factor"`
	InsurgFactor     float64 `json:"insurg_factor"`
	MerchantLength   float64 `json:"merchant_length"`

	// Planet map dimensions
	MaxX    int     `json:"max_x"`    // planet grid width
	MaxY    int     `json:"max_y"`    // planet grid height
	RatioXY float64 `json:"ratio_xy"` // map aspect ratio (x to y)

	// Victory
	VictoryPercent int `json:"victory_percent"`
	VictoryUpdates int `json:"victory_updates"`
	// Victory point weights (from tweakables.h VICT_* constants)
	VictSect    int     `json:"vict_sect"`    // sector victory weight
	VictShip    int     `json:"vict_ship"`    // ship victory weight
	VictTech    float64 `json:"vict_tech"`    // tech victory weight
	VictMorale  int     `json:"vict_morale"`  // morale victory weight
	VictRes     int     `json:"vict_res"`     // resource victory weight
	VictFuel    int     `json:"vict_fuel"`    // fuel victory weight
	VictMoney   int     `json:"vict_money"`   // money victory weight
	VictDivisor int     `json:"vict_divisor"` // total divisor for victory calculation

	// Commodity market
	UpBid float64 `json:"up_bid"` // bid increment for market

	// Telegram
	AutoTelegSize    int     `json:"auto_teleg_size"`
	TelegTransRPCost int     `json:"teleg_trans_rp_cost"`
	TelegTransAmt    float64 `json:"teleg_trans_amt"`
	TelegLetters     int     `json:"teleg_letters"`
	TelegMaxAuto     int     `json:"teleg_max_auto"`

	// Universe generation
	CreatUnivIterat int `json:"creat_univ_iterat"`
}

// Default returns a Config pre-filled with the original tweakables.h values
// and safe operational defaults. It deliberately has no JWT secret and no
// admin token: both must be supplied through the environment (validate.go).
func Default() *Config {
	return &Config{
		Host:      "localhost",
		Port:      8080,
		AdminAddr: "127.0.0.1:8081",
		DBPath:    "./gb.db",
		DataDir:   "./Data",
		DocsDir:   "./Docs",
		TeleDir:   "./Tele",
		NewsDir:   "./News",

		LogLevel:               "info",
		ShutdownTimeoutSeconds: 30,
		TurnStallTimeoutSecs:   600,
		WSMaxMessageBytes:      4096,
		WSWriteTimeoutSeconds:  10,
		WSOutboundQueue:        256,

		CommandTimeMSec:  250,
		CommandsPerTime:  1,
		CommandBurstSize: 250,
		DisconnectTime:   7200,

		DefaultUpdateMinutes:      30,
		DefaultRandomUpdateRange:  0,
		DefaultRandomSegmentRange: 0,
		MovesPerUpdate:            3,

		Market:  true,
		Defense: true,
		Voting:  true,
		Monitor: true,

		MoveFactor:         1,
		LimitAPs:           255,
		MaxCommStrSize:     250,
		CommandSize:        42,
		MaxArgs:            256,
		LoginNameSize:      64,
		NumTimesToWaitLock: 200,

		UnivSize:    150000,
		SystemSize:  2000,
		PLOrbitSize: 50,
		MaxSectPopn: 32767,
		ToxMax:      20,

		MassFuel:     0.05,
		MassResource: 0.1,
		MassDestruct: 0.15,
		MassArmor:    1.0,
		MassSize:     0.2,
		MassHanger:   0.1,
		MassGuns:     0.2,

		SizeGuns:     0.1,
		SizeCrew:     0.01,
		SizeResource: 0.02,
		SizeFuel:     0.01,
		SizeDestruct: 0.02,

		ResourceProduction: 0.00008,
		FuelProduction:     0.00008,
		DestProduction:     0.00008,
		PopnProd:           0.3,
		EffProd:            0.20,
		FactorFertSupport:  1,
		HabitatProdRate:    0.05,
		HabitatPopRate:     0.20,

		SectorDamage:     0.3,
		ShipDamage:       2.0,
		FactorDamage:     2.0,
		FactorDestPlanet: 0.35,
		PlanFireLim:      20,
		MechAttack:       3.0,

		FuelUse:               0.02,
		HyperDriveFuelUse:     5.0,
		HyperDistFactor:       200.0,
		HyperDriveReadyCharge: 1,
		LaunchGravMassFactor:  0.18,
		LandGravMassFactor:    0.0145,
		FuelGasAdd:            5.0,
		FuelGasAddTanker:      100.0,
		FuelGasAddHabitat:     200.0,
		FuelGasAddStation:     100.0,
		DistToLand:            10.0,
		DistToDock:            10.0,
		StrikeDistanceFactor:  5.5,
		ComplexityFactor:      10.0,
		GravFactor:            0.0025,
		PlanetGravConst:       0.05,
		SystemGravConst:       150000.0,

		TechInvest:       0.01,
		TechScale:        2.0,
		TechSeeStability: 15,
		TechExplore:      10,

		PopnMoveScale1: 400,
		PopnMoveScale2: 3000,
		ShipMoveScale:  3.0,

		FuelCostTerra:  3.0,
		FuelCostQuarry: 2.0,
		FuelCostPlow:   2.0,
		ResCostDome:    1,
		ResCostWPlant:  1,
		FuelCostWPlant: 1.0,
		EnvirDamageTox: 70,

		EnlistTroopCost: 5,
		UpdateTroopCost: 1,
		MobCost:         0.00,
		FactorMobProd:   0.06,
		FuelManeuver:    0.3,

		RepairRate:       25.0,
		SectorRepairCost: 10,
		NaturalRepair:    5,

		VNResTake:        0.5,
		APFactor:         50.0,
		Dissipate:        80,
		PodThreshold:     18,
		PodDecay:         4,
		SporeSuccessRate: 25,
		MaxGovernors:     5,
		MesoPopScale:     20000.0,
		IncomeFactor:     0.002,
		InsurgFactor:     1,
		MerchantLength:   200000.0,

		MaxX:    45,
		MaxY:    19,
		RatioXY: 3.70,

		VictoryPercent: 10,
		VictoryUpdates: 5,
		VictSect:       1000,
		VictShip:       333,
		VictTech:       0.10,
		VictMorale:     200,
		VictRes:        100,
		VictFuel:       15,
		VictMoney:      5,
		VictDivisor:    10000,

		UpBid: 0.10,

		AutoTelegSize:    2000,
		TelegTransRPCost: 5,
		TelegTransAmt:    0.45,
		TelegLetters:     7,
		TelegMaxAuto:     7,

		CreatUnivIterat: 10,
	}
}
