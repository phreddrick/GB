package model

// Power holds the aggregated power report for one player.
// Corresponds to struct power in power.h; regenerated each update.
type Power struct {
	Troops       uint64 `json:"troops"`
	Popn         uint64 `json:"popn"`
	Resource     uint64 `json:"resource"`
	Fuel         uint64 `json:"fuel"`
	Destruct     uint64 `json:"destruct"`
	ShipsOwned   uint16 `json:"ships_owned"`
	PlanetsOwned uint16 `json:"planets_owned"`
	SectorsOwned uint64 `json:"sectors_owned"`
	Money        uint64 `json:"money"`
	SumMob       uint64 `json:"sum_mob"`
	SumEff       uint64 `json:"sum_eff"`
}

// PowerBlocks holds the block-level power report.
// Corresponds to struct power_blocks in races.h.
type PowerBlocks struct {
	Time         string             `json:"time"`
	Members      [MaxPlayers]uint64 `json:"members"`
	Troops       [MaxPlayers]uint64 `json:"troops"`
	Popn         [MaxPlayers]uint64 `json:"popn"`
	Resource     [MaxPlayers]uint64 `json:"resource"`
	Fuel         [MaxPlayers]uint64 `json:"fuel"`
	Destruct     [MaxPlayers]uint64 `json:"destruct"`
	ShipsOwned   [MaxPlayers]uint16 `json:"ships_owned"`
	SystemsOwned [MaxPlayers]uint16 `json:"systems_owned"`
	SectorsOwned [MaxPlayers]uint64 `json:"sectors_owned"`
	Money        [MaxPlayers]uint64 `json:"money"`
	VPs          [MaxPlayers]uint16 `json:"vps"`
}
