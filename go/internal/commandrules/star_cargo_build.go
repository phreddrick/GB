package commandrules

import (
	"fmt"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/model"
)

// StarCargoBuild describes an ordinary Cargo ship building one or two ships
// of an allowed design in star orbit. Ships witnesses the entire exterior chain.
type StarCargoBuild struct {
	CargoBuild
	Kind  model.ShipType
	Count int
}

func (in StarCargoBuild) design(cfg *config.Config) (builder, design *model.Ship, err error) {
	r, p, g := in.Race, in.Player, in.Governor
	if cfg == nil || in.Count < 1 || in.Count > 2 || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors ||
		r == nil || r.PlayerNum != p || r.God || in.Scope.Level != model.LevelShip || in.Planet != nil || in.Universe != nil {
		return nil, nil, ErrFactoryBuildInput
	}
	switch in.Kind {
	case model.StypeStation, model.StypeHabitat, model.StypeOAP, model.StypeMirror:
	case model.StypeCarrier:
		// A request for two may commit an affordable prefix of one. A
		// request for one uses the separate single-Carrier rules.
		if in.Count != 2 {
			return nil, nil, ErrFactoryBuildInput
		}
	default:
		return nil, nil, ErrFactoryBuildInput
	}
	orbit, err := StarExterior(in.Star, in.Ships)
	if err != nil {
		return nil, nil, err
	}
	b := orbit[in.Scope.ShipNo]
	if b == nil || b.Type != model.StypeCargo {
		return nil, nil, ErrFactoryBuildInput
	}
	// As in buildAtShip/testShip, ownership is by race, not by the
	// builder's governor. The child's governor is the issuing governor.
	if int(b.Owner) != p || !b.Alive || !b.Active || b.Docked || b.Popn == 0 || b.Damage != 0 {
		return nil, nil, &FactoryBuildRefusal{"You can't build here.\n"}
	}
	sd := model.Shipdata[in.Kind]
	if sd[model.AbilBuild]&model.Shipdata[b.Type][model.AbilConstruct] == 0 {
		return nil, nil, &FactoryBuildRefusal{fmt.Sprintf("This ship type cannot be built by a %s.\n", model.ShipNames[b.Type])}
	}
	tech := float64(sd[model.AbilTech])
	if r.Tech < tech {
		return nil, nil, &FactoryBuildRefusal{fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n", tech, r.Tech)}
	}
	sh, err := constructionDesign(cfg, r, in.Kind)
	if err != nil || sh.BuildCost == 0 || !finiteCargoMass(b.Fuel) {
		return nil, nil, ErrFactoryBuildInput
	}
	return b, sh, nil
}

func (in StarCargoBuild) Affordable(cfg *config.Config) (int, error) {
	b, sh, err := in.design(cfg)
	if err != nil {
		return 0, err
	}
	return min(in.Count, int(b.Resource/sh.BuildCost)), nil
}

type StarCargoChange struct {
	Builder *model.Ship
	Ships   []*model.Ship
	Notice  string
}

// PlanStarCargoBuild preserves the affordable prefix and the existing exterior
// placement. Each child loads from the remaining builder inventory in order.
func PlanStarCargoBuild(in StarCargoBuild, numbers []uint32, cfg *config.Config) (StarCargoChange, error) {
	var out StarCargoChange
	b, design, err := in.design(cfg)
	if err != nil {
		return out, err
	}
	count := min(in.Count, int(b.Resource/design.BuildCost))
	if len(numbers) != count {
		return out, ErrFactoryBuildInput
	}
	if count < in.Count {
		out.Notice = fmt.Sprintf("You need %dr to construct the ship.\n", design.BuildCost)
	}
	next := *b
	head := in.Star.Ships
	used := map[uint32]bool{0: true}
	for _, sh := range in.Ships {
		used[sh.Number] = true
	}
	for _, id := range numbers {
		if used[id] {
			return StarCargoChange{}, ErrFactoryBuildInput
		}
		used[id] = true
		template := *design
		child, err := loadExteriorConstruction(cfg, in.Race, in.Player, in.Governor, &next, &template, id, head)
		if err != nil {
			return StarCargoChange{}, err
		}
		out.Ships = append(out.Ships, child)
		next.Resource -= child.BuildCost
		next.Popn -= child.Popn
		next.Fuel -= child.Fuel
		head = id
	}
	out.Builder = &next
	return out, nil
}
