package game

import (
	"errors"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) handleStarCargoBuild(p, g int, args []string, sess net.Responder) bool {
	if len(args) < 1 || len(args) > 2 || p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return false
	}
	var kind model.ShipType
	switch args[0] {
	case "S":
		kind = model.StypeStation
	case "H":
		kind = model.StypeHabitat
	case "O":
		kind = model.StypeOAP
	case "M":
		kind = model.StypeMirror
	case "X":
		if len(args) != 2 || args[1] != "2" {
			return false
		}
		kind = model.StypeCarrier
	default:
		return false
	}
	count := 1
	if len(args) == 2 {
		switch args[1] {
		case "1":
		case "2":
			count = 2
		default:
			return false
		}
	}
	race := s.raceByNum(p)
	dir := s.dirs[p-1][g]
	b := s.ships[dir.ShipNo]
	if race == nil || race.God || dir.Level != model.LevelShip || b == nil || b.Type != model.StypeCargo || b.WhatOrbits != model.LevelStar {
		return false
	}
	// Keep the existing eligibility responses. Ineligible Cargo ships
	// cannot construct on the legacy route either.
	var st, pl int
	if s.buildAtShip(p, g, race, b, &st, &pl) < 0 {
		return false
	}
	s.runShipConstruction(p, g, sess, func(p, g int) (stagedShipBuild, bool, error) {
		return s.stageStarCargoBuild(p, g, kind, count)
	})
	return true
}

func (s *State) stageStarCargoBuild(p, g int, kind model.ShipType, count int) (stagedShipBuild, bool, error) {
	w := newCommandWorkspace(s)
	dir := s.dirs[p-1][g]
	w.scope = &workspaceScope{p, g, dir}
	r, err := w.race(p)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	b, err := w.ship(dir.ShipNo)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	star, err := w.star(b.StOrbits)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	orbit, err := constructionChain(star.Ships, w.ship)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	in := commandrules.StarCargoBuild{
		CargoBuild: commandrules.CargoBuild{Player: p, Governor: g, Scope: dir, Race: r, Star: star, Ships: orbit},
		Kind:       kind,
		Count:      count,
	}
	affordable, err := in.Affordable(s.cfg)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	pending, err := s.starPeerAdmission(w, star, r, orbit, nil, nil)
	if err != nil || pending {
		return stagedShipBuild{}, pending, err
	}
	var tickets []db.FactoryAllocation
	var numbers []uint32
	if affordable != 0 {
		tickets, err = s.store.StarCargoBuildCandidates(affordable)
		var ce *db.CommandError
		if errors.As(err, &ce) && ce.Code == db.CommandStale {
			return stagedShipBuild{}, true, nil
		}
		if err != nil {
			return stagedShipBuild{}, false, err
		}
		for _, ticket := range tickets {
			if s.ships[ticket.Number] != nil {
				return stagedShipBuild{}, true, nil
			}
			numbers = append(numbers, ticket.Number)
		}
	}
	change, err := commandrules.PlanStarCargoBuild(in, numbers, s.cfg)
	if err != nil {
		return stagedShipBuild{}, false, err
	}
	if affordable == 0 {
		return stagedShipBuild{}, false, &commandrules.FactoryBuildRefusal{Message: change.Notice}
	}
	return stagedShipBuild{
		workspace: w, children: change.Ships, notice: change.Notice,
		commit: func() (db.CommandOutcome, error) {
			return s.store.CommitStarCargoBuild(db.StarCargoBuild{Input: in, Allocations: tickets, Ships: change.Ships, Config: s.cfg})
		},
	}, false, nil
}
