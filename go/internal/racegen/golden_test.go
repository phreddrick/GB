package racegen

import (
	"fmt"
	"testing"

	"github.com/phreddrick/GB/go/internal/testutil"
)

func TestOriginalCCosts(t *testing.T) {
	rows := testutil.LoadGolden(t, "racegen_cost")
	for _, row := range rows {
		t.Run(row.Str(t, "case"), func(t *testing.T) {
			s := Default()
			s.RaceType = RaceType(row.Int(t, "race_type"))
			s.Homeworld = Homeworld(row.Int(t, "home"))
			var attrs [AttributeCount]float64
			for i := range attrs {
				attrs[i] = row.Float(t, fmt.Sprintf("a%d", i))
			}
			s.Attributes = AttributesFromValues(attrs)
			for i := range s.SectorCompat {
				s.SectorCompat[i] = row.Float(t, fmt.Sprintf("s%d", i))
			}
			b, errs := Cost(s)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			for i, v := range b.Attributes {
				if want := row.Int(t, fmt.Sprintf("ac%d", i)); v != want {
					t.Errorf("attribute %d: Go %d C %d", i, v, want)
				}
			}
			for i, v := range b.Sectors {
				if want := row.Int(t, fmt.Sprintf("sc%d", i)); v != want {
					t.Errorf("sector %d: Go %d C %d", i, v, want)
				}
			}
			for col, v := range map[string]int{"home_cost": b.Homeworld, "surcharge": b.SectorSurcharge, "total": b.Total, "remaining": b.Remaining} {
				if want := row.Int(t, col); v != want {
					t.Errorf("%s: Go %d C %d", col, v, want)
				}
			}
			if b.EightTypeExtension {
				t.Fatal("historical C fixture used undefined eight-type case")
			}
		})
	}
	t.Logf("%d defined original-C designs matched all cost components", len(rows))
}

func TestOriginalCRounding(t *testing.T) {
	for _, r := range testutil.LoadGolden(t, "racegen_round") {
		got, ok := cRound(r.Float(t, "input"))
		if !ok || got != r.Int(t, "rounded") {
			t.Errorf("%v: got %d, %t", r, got, ok)
		}
	}
}

func TestOriginalCMetadata(t *testing.T) {
	for _, r := range testutil.LoadGolden(t, "racegen_metadata") {
		i := r.Int(t, "attribute")
		kind := RaceType(r.Int(t, "kind"))
		d := AttributeDefinitions()[i]
		name := d.Name
		if kind == Metamorph && Attribute(i) == IQ {
			name = "IQ Limit"
		}
		if name != r.Str(t, "name") {
			t.Errorf("name: %s vs %s", name, r.Str(t, "name"))
		}
		for col, v := range map[string]float64{"min": d.Minimum, "default": d.Default, "max": d.Maximum, "fudge": float64(Calibration()[i])} {
			if v != r.Float(t, col) {
				t.Errorf("attribute %d %s: Go %g C %g", i, col, v, r.Float(t, col))
			}
		}
		if d.Kind != [3]NumberKind{Decimal, Integer, Boolean}[r.Int(t, "integral")] {
			t.Errorf("attribute %d kind mismatch", i)
		}
		for j, v := range Covariances(kind)[i] {
			if want := r.Float(t, fmt.Sprintf("cov%d", j)); v != want {
				t.Errorf("kind %d covariance %d,%d: Go %.17g C %.17g", kind, i, j, v, want)
			}
		}
	}
	for _, r := range testutil.LoadGolden(t, "racegen_homeworld") {
		i := r.Int(t, "home")
		h := Homeworlds()[i]
		if h.Name != r.Str(t, "name") || int(h.PlanetType) != r.Int(t, "planet_type") || h.Cost != r.Int(t, "cost") {
			t.Errorf("homeworld %d mismatch: %+v", i, h)
		}
		for j, v := range planetCovariances()[i] {
			if v != r.Float(t, fmt.Sprintf("cov%d", j)) || h.CommonSectors[j] != (v == 1) {
				t.Errorf("homeworld %d sector %d covariance mismatch", i, j)
			}
		}
	}
}
