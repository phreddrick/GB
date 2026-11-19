package game

import (
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

// Assert the ecological precondition rather than trying to seed or suppress
// normal turn randomness. A capture-persistence test must not also depend on
// a lucky starvation draw. All original dispatch/persistence assertions stay.
func TestGroundAssaultCaptureFixtureSupport(t *testing.T) {
	t.Parallel()
	f := newGroundAssaultFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.star][f.planet]
		r := f.state.races[0]
		sec := p.Sectors[1]
		if sec.Owner != 2 || sec.Popn != 1 || sec.Troops != 0 || sec.Fert != 100 || r.BirthRate != 0 || f.state.stars[f.star].NovaStage != 0 || p.Conditions[model.AtmosToxic] != 0 {
			t.Error("capture fixture changed its ownership/population/ecology preconditions")
		}
		const arrivals = 1000
		tox := int(p.Conditions[model.AtmosToxic])
		if r.BirthRate != 0 || sec.Fert != 100 || tox != 0 || f.state.stars[f.star].NovaStage != 0 {
			t.Error("capture-retention fixture needs zero birth growth, full fertility and a clean non-nova planet")
		}
		initial := maxSupport(r, &sec, compatibility(p, r), tox)
		minimum := initial
		// doPlanet caches compatibility before changing temperature. Check
		// both that initial value and every possible next-temperature value,
		// so a later update's compatibility also leaves room for arrivals.
		for offset := int16(-5); offset <= 5; offset++ {
			next := *p
			next.Conditions[model.AtmosTemp] = p.Conditions[model.AtmosRTemp] + offset
			support := maxSupport(r, &sec, compatibility(&next, r), tox)
			minimum = min(minimum, support)
		}
		if minimum < arrivals {
			t.Errorf("capture fixture cannot support %d civilians: initial=%d minimum=%d", arrivals, initial, minimum)
		}
		// The observed old fixture grew from Eff 0 to 1 before production.
		// Its legal starvation range included the whole arriving population.
		// This is pure arithmetic using the production capacity function,
		// not an RNG override or a synthetic claim of a real random draw.
		legacy := sec
		legacy.Eff = 1
		oldSupport := maxSupport(r, &legacy, compatibility(p, r), tox)
		diff := arrivals - oldSupport
		if diff <= 0 || min(2*diff, arrivals) != arrivals {
			t.Error("old fixture no longer exposes the reproduced full-starvation range")
		}
		t.Logf("arrivals=%d support: initial=%d minimum=%d; legacy support=%d starvation upper bound=%d", arrivals, initial, minimum, oldSupport, min(2*diff, arrivals))
	})
}
