package game

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

func TestStockpileTransferDurableAP(t *testing.T) {
	f := newPlanetCommandFixture(t)
	planetBefore := f.storedPlanet(t)
	starBefore, err := f.store.GetStar(f.star)
	if err != nil {
		t.Fatal(err)
	}
	wantPlanet := planetEdit(planetBefore)
	wantPlanet.Info[0].Crystals -= 7
	wantPlanet.Info[1].Crystals += 7
	wantStar := *starBefore
	wantStar.AP[0]--
	out := f.command(t, "transfer 2 x 7")
	var actorStar model.Star
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { actorStar = *f.state.stars[f.star] })
	gotStar, err := f.store.GetStar(f.star)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output=%q; star AP before=%d actor=%d stored=%d; crystals stored=%d/%d", out, starBefore.AP[0], actorStar.AP[0], gotStar.AP[0], f.storedPlanet(t).Info[0].Crystals, f.storedPlanet(t).Info[1].Crystals)
	for name, record := range map[string]any{"planet-before": planetBefore, "star-before": starBefore, "planet-after": f.storedPlanet(t), "star-actor-after": actorStar, "star-store-after": gotStar} {
		b, e := json.Marshal(record)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("%s=%s", name, b)
	}
	if !reflect.DeepEqual(f.storedPlanet(t), wantPlanet) || !reflect.DeepEqual(f.actorPlanet(t), wantPlanet) || !reflect.DeepEqual(gotStar, &wantStar) || !reflect.DeepEqual(&actorStar, &wantStar) {
		t.Fatal("acknowledged stockpile transfer did not atomically persist complete planet AND star AP")
	}
}
