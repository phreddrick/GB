package game

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) handleProbeScrap(p, g int, spec string, sh *model.Ship, sess net.Responder) bool {
	if spec == "" || (spec[0] != '#' && (spec[0] < '0' || spec[0] > '9')) || !commandrules.SoloScrapProbe(sh) {
		return false
	}
	if race := s.raceByNum(p); race != nil && race.GovShip == sh.Number {
		return false // legacy killShip also clears the race's government ship
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(spec, "#"), "+"), 10, 32)
	if err != nil || id == 0 {
		_ = sess.Send("Invalid ship number.\n")
		return true
	}
	s.scrapProbe(p, g, uint32(id), sh.StOrbits, sess)
	return true
}

func (s *State) scrapProbe(p, g int, id uint32, starNum uint8, sess net.Responder) {
	outcome := db.CommandNotCommitted
	halt := func() {
		s.halt(fmt.Sprintf("probe scrap %s; reconciliation required; no shutdown flush", outcome), "")
		_ = sess.Send(fmt.Sprintf("Scrap %s; engine halted for reconciliation.\n", outcome))
	}
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("probe scrap %s; panic; no shutdown flush", outcome), "")
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return
	}
	if s.store.CommandWriteUncertain() {
		outcome = db.CommandUncertain
		halt()
		return
	}
	w := newCommandWorkspace(s)
	race, err := w.race(p)
	if err != nil {
		halt()
		return
	}
	st, err := w.star(starNum)
	if err != nil {
		halt()
		return
	}
	orbit, err := constructionChain(st.Ships, w.ship)
	if err != nil {
		halt()
		return
	}
	in := commandrules.ProbeScrap{Player: p, Governor: g, Selected: id, Race: race, Star: st, Orbit: orbit}
	dead, star, planErr := commandrules.PlanProbeScrap(in)
	if planErr == commandrules.ErrUndockOutside {
		// Resolve a historical hold or partially saved construction before
		// deciding whether the missing exterior membership is corruption.
		members, err := commandrules.StarExterior(st, orbit)
		if err != nil {
			halt()
			return
		}
		planErr = w.absentStarSelections(st, members, id)
	}
	switch planErr {
	case nil, commandrules.ErrScrapAP, commandrules.ErrUndockAuthority, commandrules.ErrUndockUnsettled:
	default:
		halt()
		return
	}
	pending, err := s.starPeerAdmission(w, st, race, orbit, nil, nil)
	if err != nil || !w.owns() {
		halt()
		return
	}
	switch {
	case pending:
		_ = sess.Send("Scrap deferred: retry after the next persisted turn.\n")
		return
	case planErr == commandrules.ErrScrapAP:
		s.enoughAP(p, g, st.AP[p-1], 1) // legacy registry delivery to this governor
		return
	case planErr == commandrules.ErrUndockAuthority:
		_ = sess.Send("You are not authorized to scrap this ship.\n")
		return
	case planErr == commandrules.ErrUndockUnsettled:
		_ = sess.Send("Cannot scrap: star orbit records are unsettled; retry after the next persisted turn.\n")
		return
	}
	outcome, err = s.store.CommitProbeScrap(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			halt()
			return
		}
		*w.ships[id].actor = *dead
		*w.stars[starNum].actor = *star
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if err == nil && sess.Send(fmt.Sprintf("%s is not landed or docked.\nNo resources can be reclaimed.\n", shipDisplayName(dead))) == nil && sess.Send("\nDestroyed.\n") == nil {
			return
		}
	}
	halt()
}
