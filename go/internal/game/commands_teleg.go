package game

// commands_teleg.go implements:
//   cmdTelegram  — send a telegram to a player / all players
//   cmdRead      — read pending telegrams and news streams
//
// C sources ported: tele.c, teleg_send.c, name.c (send_message / read_messages)

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// cmdTelegram sends a message to another player (or all players).
//
// Syntax (mirrors send_message in name.c):
//   telegram <player#|name> <message...>
//   telegram <player#> <governor#> <message...>
//   telegram * <message...>      — broadcast to all races
//   telegram star <starname> <message...> — stargram
//
// The original C command is "send"; we expose it as "telegram" to match the
// stub name, and also register "send" as an alias in the dispatch table.
func (s *State) cmdTelegram(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: telegram <player> [governor] <message...>\n")
		sess.Send("       telegram * <message...>  (broadcast to all)\n")
		sess.Send("       telegram star <name> <message...>\n")
		return
	}

	senderRace := s.raceByNum(p)
	if senderRace == nil {
		sess.Send("Internal error: your race not found.\n")
		return
	}

	dir := s.dirs[p-1][g]
	if dir.Level == model.LevelUniv {
		sess.Send("You can't send messages from universal scope.\n")
		return
	}
	if dir.Level == model.LevelShip {
		sess.Send("You can't send messages from ship scope.\n")
		return
	}

	target := args[0]
	msgStart := 1

	// ── broadcast to all ──────────────────────────────────────────────────
	if target == "*" {
		msgParts := args[msgStart:]
		if len(msgParts) == 0 {
			sess.Send("Broadcast what?\n")
			return
		}
		body := fmt.Sprintf("%s \"%s\" [%d,%d]: %s\n",
			senderRace.Name, senderRace.Governor[g].Name,
			p, g, strings.Join(msgParts, " "))
		for i := 1; i <= len(s.races); i++ {
			if r := s.raceByNum(i); r != nil && i != p {
				s.pushTelegramRace(i, body)
			}
		}
		sess.Send("Broadcast sent.\n")
		return
	}

	// ── stargram ──────────────────────────────────────────────────────────
	if strings.EqualFold(target, "star") {
		if len(args) < 3 {
			sess.Send("Usage: telegram star <starname> <message...>\n")
			return
		}
		starName := args[1]
		msgParts := args[2:]
		var starNum int = -1
		for i, st := range s.stars {
			if st != nil && strings.EqualFold(st.Name, starName) {
				starNum = i
				break
			}
		}
		if starNum < 0 {
			sendf(sess, "No such star %q.\n", starName)
			return
		}
		star := s.stars[starNum]
		body := fmt.Sprintf("%s \"%s\" [%d,%d] to inhabitants of %s: %s\n",
			senderRace.Name, senderRace.Governor[g].Name,
			p, g, star.Name, strings.Join(msgParts, " "))
		notice := fmt.Sprintf("%s \"%s\" [%d,%d] sends a stargram to %s.\n",
			senderRace.Name, senderRace.Governor[g].Name, p, g, star.Name)
		for i := 1; i <= len(s.races); i++ {
			if r := s.raceByNum(i); r != nil {
				if star.IsInhabited(i) && i != p {
					s.notifyRace(i, notice)
					s.pushTelegramRace(i, body)
				}
			}
		}
		sess.Send("Stargram sent.\n")
		return
	}

	// ── direct telegram ───────────────────────────────────────────────────
	// Resolve recipient: number or name.
	who := 0
	if n, err := strconv.Atoi(target); err == nil {
		if n >= 1 && n <= len(s.races) && s.races[n-1] != nil {
			who = n
		}
	} else {
		// name match
		for i, r := range s.races {
			if r != nil && strings.EqualFold(r.Name, target) {
				who = i + 1
				break
			}
		}
	}
	if who == 0 {
		sendf(sess, "No such player %q.\n", target)
		return
	}

	// Optional explicit governor number as second arg.
	govTarget := -1 // -1 = send to all active governors
	if len(args) >= 2 {
		if gn, err := strconv.Atoi(args[1]); err == nil && gn >= 0 && gn <= model.MaxGovernors {
			govTarget = gn
			msgStart = 2
		}
	}

	msgParts := args[msgStart:]
	if len(msgParts) == 0 {
		sess.Send("Send what message?\n")
		return
	}
	body := fmt.Sprintf("%s \"%s\" [%d,%d]: %s\n",
		senderRace.Name, senderRace.Governor[g].Name,
		p, g, strings.Join(msgParts, " "))
	notice := fmt.Sprintf("%s \"%s\" [%d,%d] has sent you a telegram. Use 'read' to read it.\n",
		senderRace.Name, senderRace.Governor[g].Name, p, g)

	if govTarget >= 0 {
		s.pushTelegram(who, govTarget, body)
		_ = s.registry != nil && s.registry.Notify(who, govTarget, notice)
	} else {
		s.pushTelegramRace(who, body)
		s.notifyRace(who, notice)
	}

	// Update translation modifier on the recipient race.
	if alien := s.raceByNum(who); alien != nil {
		if alien.Translate[p-1] < 100 {
			alien.Translate[p-1] += 2
			if alien.Translate[p-1] > 100 {
				alien.Translate[p-1] = 100
			}
			_ = s.store.PutRace(alien)
		}
	}

	sess.Send("Message sent.\n")
}

// cmdRead displays pending telegrams and / or the news streams.
//
// Syntax (mirrors read_messages in name.c):
//   read              — read pending telegrams (then clear them)
//   read telegram     — same as above
//   read news         — read all four news streams from last-read position
//
// The governor's NewsPos cursor in the Race record is updated after reading
// news so the player only sees new entries next time.
func (s *State) cmdRead(p, g int, args []string, sess net.Responder) {
	sub := "telegram"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "telegram", "telegrams", "":
		s.readTelegrams(p, g, sess)
	case "news":
		s.readNews(p, g, sess)
	default:
		sess.Send("Read what? (telegram | news)\n")
	}
}

// readTelegrams displays and clears the pending telegram inbox.
// Mirrors teleg_read() in tele.c / read_teleg.c.
func (s *State) readTelegrams(p, g int, sess net.Responder) {
	msgs, err := s.store.ReadAndClearTelegrams(p, g)
	if err != nil {
		sendf(sess, "Error reading telegrams: %v\n", err)
		return
	}
	sess.Send("Telegrams:\n")
	if len(msgs) == 0 {
		sess.Send(" None.\n")
		return
	}
	sess.Send("\n")
	for _, m := range msgs {
		sess.Send(m)
		if len(m) == 0 || m[len(m)-1] != '\n' {
			sess.Send("\n")
		}
	}
}

// readNews displays unread entries from the four news streams.
// Mirrors news_read() in tele.c / read_teleg.c + read_messages in name.c.
func (s *State) readNews(p, g int, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		sess.Send("Internal error: race not found.\n")
		return
	}

	sections := []struct {
		label    string
		newsType uint8
	}{
		{"Declarations", model.NewsDeclaration},
		{"Combat", model.NewsCombat},
		{"Business", model.NewsTransfer},
		{"Bulletins", model.NewsAnnounce},
	}

	anyNews := false
	for _, sec := range sections {
		fromSeq := race.Governor[g].NewsPos[sec.newsType]

		// Guard against stale cursor pointing past end.
		latest, _ := s.store.LatestNewsSeq(sec.newsType)
		if fromSeq > latest {
			fromSeq = 0
		}

		entries, newSeq, err := s.store.ReadNewsFrom(sec.newsType, fromSeq)
		if err != nil {
			sendf(sess, "Error reading %s news: %v\n", sec.label, err)
			continue
		}
		if len(entries) == 0 {
			continue
		}
		anyNews = true
		sendf(sess, "\n----------  %s  ----------\n", sec.label)
		for _, e := range entries {
			sess.Send(e)
			if len(e) == 0 || e[len(e)-1] != '\n' {
				sess.Send("\n")
			}
		}
		// Advance cursor.
		if newSeq > race.Governor[g].NewsPos[sec.newsType] {
			race.Governor[g].NewsPos[sec.newsType] = newSeq
		}
	}
	if !anyNews {
		sess.Send("No new news.\n")
	}
	// Persist updated news positions.
	_ = s.store.PutRace(race)
}

// checkForTelegrams notifies a player on login if they have waiting telegrams.
// Called from state.go after authentication.  Mirrors check_for_telegrams() in
// GB_server.c.
func (s *State) checkForTelegrams(playerNum, governor int, sess net.Responder) {
	has, err := s.store.HasTelegrams(playerNum, governor)
	if err != nil || !has {
		return
	}
	sess.Send("You have telegram(s) waiting. Use 'read' to read them.\n")
}
