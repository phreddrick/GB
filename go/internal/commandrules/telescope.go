// Package commandrules holds pure rules shared by actor staging and the closed
// storage validators. It does not read or write game state.
package commandrules

import (
	"errors"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
)

var ErrFuel = errors.New("insufficient telescope maneuver fuel")

// TelescopeRange deliberately preserves the current Go arithmetic. Original C
// uses SYSTEMSIZE/8 and /3 instead of these additive constants; correcting that
// pre-existing balancing discrepancy is outside the persistence migration.
func TelescopeRange(kind model.ShipType, tech float64) float64 {
	if kind == model.OtypeGtele {
		return math.Log1p(tech)*400 + model.SystemGravConst/375
	}
	return math.Log1p(tech)*1500 + model.SystemGravConst/100
}

type TelescopeChange struct {
	Aim                         model.AimedAtSpecial
	Fuel, Mass, Distance, Range float64
	Explore                     bool
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// PlanTelescope preserves order.c aim semantics, including paid out-of-range,
// universe and ship aims. The caller resolves/authorizes identities separately.
// Rates come only from the authoritative server configuration, never the client.
func PlanTelescope(ship *model.Ship, target model.Place, star *model.Star, planet *model.Planet, maneuver, massFuel float64) (TelescopeChange, error) {
	var out TelescopeChange
	invalid := errors.New("invalid telescope rule inputs")
	if ship == nil || (ship.Type != model.OtypeStele && ship.Type != model.OtypeGtele) || !finite(ship.Tech) || ship.Tech < 0 || !finite(ship.XPos) || !finite(ship.YPos) || !finite(ship.Fuel) || ship.Fuel < 0 || !finite(ship.Mass) || ship.Mass < 0 || !finite(maneuver) || maneuver < 0 || !finite(massFuel) || massFuel < 0 || target.Err {
		return out, invalid
	}
	if ship.Special.AimedAt != nil {
		out.Aim = *ship.Special.AimedAt
	}
	out.Aim.Level = target.Level
	out.Aim.StarNum = target.StarNum
	out.Aim.PlanetNum = target.PlanetNum
	if target.ShipNo > math.MaxUint16 {
		return out, invalid
	}
	out.Aim.ShipNo = uint16(target.ShipNo)
	out.Fuel = ship.Fuel
	out.Mass = ship.Mass
	if ship.Type == model.OtypeStele {
		if ship.Fuel < maneuver {
			return out, ErrFuel
		}
		out.Fuel -= maneuver
		out.Mass -= maneuver * massFuel
		if !finite(out.Mass) || out.Mass < 0 {
			return out, invalid
		}
	}
	out.Range = TelescopeRange(ship.Type, ship.Tech)
	switch target.Level {
	case model.LevelUniv:
		if star != nil || planet != nil {
			return out, invalid
		}
	case model.LevelShip:
		if target.ShipNo == 0 || star != nil || planet != nil {
			return out, invalid
		}
	case model.LevelStar, model.LevelPlan:
		if star == nil || star.Number != target.StarNum || !finite(star.XPos) || !finite(star.YPos) {
			return out, invalid
		}
		x, y := star.XPos, star.YPos
		if target.Level == model.LevelPlan {
			if planet == nil || planet.StarNum != target.StarNum || planet.PlanetNum != target.PlanetNum || target.PlanetNum >= star.NumPlanets || !finite(planet.XPos) || !finite(planet.YPos) {
				return out, invalid
			}
			x += planet.XPos
			y += planet.YPos
		} else if planet != nil {
			return out, invalid
		}
		dx, dy := ship.XPos-x, ship.YPos-y
		out.Distance = math.Sqrt(dx*dx + dy*dy)
		if !finite(out.Distance) || !finite(out.Range) {
			return out, invalid
		}
		out.Explore = out.Distance <= out.Range
	default:
		return out, invalid
	}
	return out, nil
}
