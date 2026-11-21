package commandrules

import (
	"math"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

const (
	gunCost     = 1.00
	crewCost    = 0.05
	cargoCost   = 0.05
	fuelCostBld = 0.05 // building fuel cost per unit
	ammoCost    = 0.05
	speedCost   = 0.50
	cewCost     = 0.003
	armorCost   = 3.50
	hangerCost  = 0.50
)

func ComponentCost(ship *model.Ship, factor float64) float64 {
	factor += gunCost * float64(ship.Primary)
	factor += gunCost * float64(ship.Secondary)
	factor += crewCost * float64(ship.MaxCrew)
	factor += cargoCost * float64(ship.MaxResource)
	factor += fuelCostBld * float64(ship.MaxFuel)
	factor += ammoCost * float64(ship.MaxDestruct)
	factor += speedCost * float64(ship.MaxSpeed) * math.Sqrt(float64(ship.MaxSpeed))
	factor += hangerCost * float64(ship.MaxHanger)
	factor += armorCost * float64(ship.Armor) * math.Sqrt(float64(ship.Armor))
	factor += cewCost * float64(int(ship.CEW)*int(ship.CEWRange))

	advantage := 0.0
	if ship.HyperDrive.Has {
		advantage += 0.5
	}
	if ship.Laser != 0 {
		advantage += 0.5
	}
	if ship.Cloak != 0 {
		advantage += 0.5
	}
	if ship.Mount != 0 {
		advantage += 0.5
	}
	factor *= math.Sqrt(1.0 + advantage)
	return factor
}

// Default carrier design, using the same component arithmetic as Getship.
func carrierDesign(cfg *config.Config, r *model.Race) (*model.Ship, error) {
	sd := model.Shipdata[model.StypeCarrier]
	// Bounds-check the static design fields at their eventual storage widths.
	var small [model.NumAbils]uint8
	for _, i := range []model.Ability{model.AbilArmor, model.AbilGuns, model.AbilPrimary, model.AbilSecondary, model.AbilMount} {
		v := sd[i]
		if v < 0 || v > math.MaxUint8 {
			return nil, ErrFactoryBuildInput
		}
		small[i] = uint8(v)
	}
	var capacity [model.NumAbils]uint16
	for _, i := range []model.Ability{model.AbilCargo, model.AbilMaxCrew, model.AbilSpeed, model.AbilHanger, model.AbilDestCap, model.AbilFuelCap} {
		v := sd[i]
		if v < 0 || v > math.MaxUint16 {
			return nil, ErrFactoryBuildInput
		}
		capacity[i] = uint16(v)
	}
	sh := &model.Ship{
		Type: model.StypeCarrier, BuildType: uint16(model.StypeCarrier), Armor: small[model.AbilArmor],
		Primary: small[model.AbilGuns], Secondary: small[model.AbilGuns], PrimType: small[model.AbilPrimary], SecType: small[model.AbilSecondary],
		MaxCrew: capacity[model.AbilMaxCrew], MaxResource: capacity[model.AbilCargo], MaxHanger: capacity[model.AbilHanger],
		MaxDestruct: capacity[model.AbilDestCap], MaxFuel: capacity[model.AbilFuelCap], MaxSpeed: capacity[model.AbilSpeed],
	}
	if sh.PrimType != 0 {
		sh.Guns = model.GunPrimary
	}
	size := DesignSize(cfg, sh)
	if size < 0 || size < int(sh.MaxHanger) || size > 65535 {
		return nil, ErrFactoryBuildInput
	}
	sh.Size = uint16(size)
	sh.BaseMass = DesignMass(cfg, sh)
	if r.God {
		sh.Mount = small[model.AbilMount]
		sh.HyperDrive.Has = sd[model.AbilJump] != 0
		if sd[model.AbilLaser] != 0 {
			sh.Laser = 1
		}
	} else {
		cost := ComponentCost(sh, float64(sd[model.AbilCost]))
		if !(cost >= 0 && cost < 65536) {
			return nil, ErrFactoryBuildInput
		}
		sh.BuildCost = uint16(cost)
	}
	return sh, nil
}
func DesignMass(cfg *config.Config, ship *model.Ship) float64 {
	return 1.0 +
		cfg.MassArmor*float64(ship.Armor) +
		cfg.MassSize*float64((int(ship.Size)-int(ship.MaxHanger))) +
		cfg.MassHanger*float64(ship.MaxHanger) +
		cfg.MassGuns*float64(ship.Primary)*float64(ship.PrimType) +
		cfg.MassGuns*float64(ship.Secondary)*float64(ship.SecType)
}
func DesignSize(cfg *config.Config, ship *model.Ship) int {
	size := 1.0 +
		cfg.SizeGuns*float64(ship.Primary) +
		cfg.SizeGuns*float64(ship.Secondary) +
		cfg.SizeCrew*float64(ship.MaxCrew) +
		cfg.SizeResource*float64(ship.MaxResource) +
		cfg.SizeFuel*float64(ship.MaxFuel) +
		cfg.SizeDestruct*float64(ship.MaxDestruct) +
		float64(ship.MaxHanger)
	return int(size)
}
