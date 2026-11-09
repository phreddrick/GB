package racegen

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
)

func validSpec() Spec { s := Default(); s.Name = "Test species"; s.SectorCompat[Land] = 100; return s }
func hasError(errs Errors, code, field string) bool {
	for _, e := range errs {
		if e.Code == code && e.Field == field {
			return true
		}
	}
	return false
}

func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, code, field string
		edit              func(*Spec)
	}{
		{"name", "REQUIRED", "name", func(s *Spec) { s.Name = " \t" }},
		{"home-low", "OUT_OF_RANGE", "homeworld", func(s *Spec) { s.Homeworld = -1 }},
		{"home-high", "OUT_OF_RANGE", "homeworld", func(s *Spec) { s.Homeworld = HomeworldCount }},
		{"race", "OUT_OF_RANGE", "race_type", func(s *Spec) { s.RaceType = 100 }},
		{"boolean", "BOOLEAN", "attributes.absorb", func(s *Spec) { s.RaceType = Metamorph; s.Attributes.Absorb = .5 }},
		{"collective", "BOOLEAN", "attributes.collective_iq", func(s *Spec) { s.Attributes.CollectiveIQ = .5 }},
		{"pods-bool", "BOOLEAN", "attributes.pods", func(s *Spec) { s.RaceType = Metamorph; s.Attributes.Pods = .5 }},
		{"iq-fraction", "INTEGER", "attributes.iq", func(s *Spec) { s.Attributes.IQ = 100.5 }},
		{"fighters-fraction", "INTEGER", "attributes.fighters", func(s *Spec) { s.Attributes.Fighters = 4.5 }},
		{"sexes-fraction", "INTEGER", "attributes.sexes", func(s *Spec) { s.Attributes.Sexes = 2.5 }},
		{"normal-absorb", "NORMAL_ABILITY", "attributes.absorb", func(s *Spec) { s.Attributes.Absorb = 1 }},
		{"normal-pods", "NORMAL_ABILITY", "attributes.pods", func(s *Spec) { s.Attributes.Pods = 1 }},
		{"plated", "PLATED_REQUIRED", "sector_compat[7]", func(s *Spec) { s.SectorCompat[Plated] = 99 }},
		{"water-low", "OUT_OF_RANGE", "sector_compat[0]", func(s *Spec) { s.SectorCompat[Sea] = -1 }},
		{"water-high", "OUT_OF_RANGE", "sector_compat[0]", func(s *Spec) { s.SectorCompat[Sea] = 101 }},
		{"water-fraction", "INTEGER", "sector_compat[0]", func(s *Spec) { s.SectorCompat[Sea] = .5 }},
		{"gas", "GAS_FORBIDDEN", "sector_compat[3]", func(s *Spec) { s.SectorCompat[Gas] = 1 }},
		{"jovian-water", "JOVIAN_TERRAIN", "sector_compat[0]", func(s *Spec) { s.Homeworld = Jovian; s.SectorCompat = [SectorCount]float64{100, 0, 0, 100} }},
		{"plated-only", "TERRAIN_COUNT", "sector_compat", func(s *Spec) { s.SectorCompat[Land] = 0 }},
		{"no-common", "COMMON_TERRAIN", "sector_compat", func(s *Spec) { s.SectorCompat[Land] = 99 }},
		{"budget", "OVER_BUDGET", "cost", func(s *Spec) { s.Attributes.Fighters = 20 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validSpec()
			tc.edit(&s)
			before := s
			errs := Validate(s)
			if !hasError(errs, tc.code, tc.field) {
				t.Fatalf("want %s %s, got %v", tc.code, tc.field, errs)
			}
			if s != before {
				t.Fatal("validation mutated input")
			}
			if got, errs := Validated(s); len(errs) == 0 || got != (Spec{}) {
				t.Fatal("invalid design returned as validated")
			}
		})
	}
	for _, kind := range []RaceType{Normal, Metamorph} {
		s := validSpec()
		s.RaceType = kind
		if kind == Metamorph {
			s.Attributes.Absorb = 1
			s.Attributes.Pods = 1
			s.Attributes.CollectiveIQ = 1
		}
		if errs := Validate(s); len(errs) > 0 {
			t.Fatalf("kind %d: %v", kind, errs)
		}
		got, errs := Validated(s)
		if len(errs) > 0 || got != s {
			t.Fatal("validated design changed")
		}
	}
	// Every home has a valid common-terrain design; no address or password.
	for _, h := range Homeworlds() {
		s := Default()
		s.Homeworld = h.ID
		if h.ID == Jovian {
			s.SectorCompat = [SectorCount]float64{}
		}
		for i, common := range h.CommonSectors {
			if common {
				s.SectorCompat[i] = 100
				break
			}
		}
		if errs := Validate(s); len(errs) > 0 {
			t.Errorf("%s: %v", h.Name, errs)
		}
	}
}

func TestNumericBoundaries(t *testing.T) {
	bad := []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64, -math.MaxFloat64}
	for i, d := range AttributeDefinitions() {
		for _, v := range append(append([]float64{}, bad...), math.Nextafter(d.Minimum, math.Inf(-1)), math.Nextafter(d.Maximum, math.Inf(1))) {
			s := validSpec()
			a := s.Attributes.Values()
			a[i] = v
			s.Attributes = AttributesFromValues(a)
			b, errs := Cost(s)
			if len(errs) == 0 || b != (Breakdown{}) {
				t.Errorf("attribute %d value %g accepted", i, v)
			}
			if len(Validate(s)) == 0 {
				t.Error("invalid numeric design validated")
			}
			if math.Float64bits(s.Attributes.Values()[i]) != math.Float64bits(v) {
				t.Fatal("numeric input mutated")
			}
		}
	}
	for i := 0; i < int(SectorCount); i++ {
		for _, v := range append(append([]float64{}, bad...), -1, 101) {
			s := validSpec()
			s.SectorCompat[i] = v
			if _, errs := Cost(s); len(errs) == 0 {
				t.Errorf("sector %d value %g accepted", i, v)
			}
			if len(Validate(s)) == 0 {
				t.Error("invalid sector validated")
			}
		}
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.MaxFloat64, float64(math.MaxInt32) + 1, float64(math.MinInt32) - 2} {
		if _, ok := cRound(v); ok {
			t.Errorf("undefined C conversion accepted: %g", v)
		}
	}
}

func TestApprovedEightTypeExtensionAndOverflow(t *testing.T) {
	// A fractional, unfinished design keeps all intermediate arithmetic defined.
	// C cannot supply an eight-type oracle; compare its seven-type prefix, whose
	// rounded sector costs are zero, and add only the approved surcharge delta.
	s := Default()
	for i := range s.SectorCompat {
		s.SectorCompat[i] = .000001
	}
	b, errs := Cost(s)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if b.SectorSurcharge != 600 || !b.EightTypeExtension || b.SectorCount != 8 {
		t.Fatalf("extension missing: %+v", b)
	}
	s.SectorCompat[Plated] = 0
	seven, errs := Cost(s)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if b.Total-seven.Total != 100 || seven.SectorSurcharge != 500 || seven.EightTypeExtension {
		t.Fatal("extension changed other arithmetic")
	}
	// Legal numeric ranges can still overflow C's int through the gas covariance.
	s = Default()
	for i := range s.SectorCompat {
		s.SectorCompat[i] = 100
	}
	if b, errs := Cost(s); !hasError(errs, "COST_OVERFLOW", "sector_compat[3]") || b != (Breakdown{}) {
		t.Fatalf("want overflow, got %+v %v", b, errs)
	}
	if len(Validate(s)) == 0 {
		t.Fatal("overflow design validated")
	}
}

func TestPureRepeatableAndMetadataIsolation(t *testing.T) {
	s := validSpec()
	before := s
	want, errs := Cost(s)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	d := AttributeDefinitions()
	d[0].Default = 999
	h := Homeworlds()
	h[0].CommonSectors[0] = false
	c := Covariances(Normal)
	c[0][5] = 999
	sec := Sectors()
	sec[0].Maximum = 0
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				got, errs := Cost(s)
				if len(errs) > 0 || got != want || len(Validate(s)) != 0 {
					t.Error("rules changed across calls")
				}
				m := s
				m.RaceType = Metamorph
				_, _ = Cost(m)
			}
		}()
	}
	wg.Wait()
	if s != before || AttributeDefinitions()[0].Default != .4 || !Homeworlds()[0].CommonSectors[0] || Sectors()[0].Maximum != 100 {
		t.Fatal("shared state mutated")
	}
}

func TestSpecJSONAndNumericTypes(t *testing.T) {
	s := validSpec()
	s.Address = "optional"
	s.Info = "species description"
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Spec
	if err = json.Unmarshal(data, &got); err != nil || got != s {
		t.Fatalf("round trip: %v", err)
	}
	for _, data := range []string{`{"attributes":{"iq":"150"}}`, `{"attributes":{"iq":true}}`, `{"attributes":{"iq":1e999}}`, `{"homeworld":0.5}`, `{"race_type":1e99}`} {
		if err := json.Unmarshal([]byte(data), &got); err == nil {
			t.Errorf("numeric type accepted: %s", data)
		}
	}
	errs := Validate(Spec{})
	data, err = json.Marshal(errs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"field"`) || !strings.Contains(string(data), `"code"`) {
		t.Fatal("errors lack field/code")
	}
}

func FuzzRulesNeverPanic(f *testing.F) {
	f.Add(uint64(0), int64(0), int64(0))
	f.Add(math.Float64bits(math.MaxFloat64), int64(-1), int64(100))
	f.Fuzz(func(t *testing.T, bits uint64, home, kind int64) {
		s := validSpec()
		s.Homeworld = Homeworld(home)
		s.RaceType = RaceType(kind)
		s.Attributes.IQ = math.Float64frombits(bits)
		s.SectorCompat[Sea] = math.Float64frombits(bits)
		_, _ = Cost(s)
		_ = Validate(s)
		if math.Float64bits(s.Attributes.IQ) != bits {
			t.Fatal("input mutated")
		}
	})
}
