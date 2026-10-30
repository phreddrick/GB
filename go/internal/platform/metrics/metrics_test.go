package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := NewCounter(r, "gb_commands_total", "Commands.", "result")
	g := NewGauge(r, "gb_engine_mode", "Mode.", "mode")
	h := NewHistogram(r, "gb_turn_duration_seconds", "Turns.", []float64{1, 10}, "kind")
	plain := NewCounter(r, "gb_panics_total", "Panics.")

	c.Inc("ok")
	c.Add(2, "ok")
	c.Inc("refused")
	g.Set(1, "SERVING")
	g.Set(0, "HALTED")
	h.Observe(0.5, "update")
	h.Observe(5, "update")
	h.Observe(50, "update")
	plain.Inc()

	var sb strings.Builder
	if _, err := r.WriteTo(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		"# TYPE gb_commands_total counter\n",
		`gb_commands_total{result="ok"} 3` + "\n",
		`gb_commands_total{result="refused"} 1` + "\n",
		`gb_engine_mode{mode="HALTED"} 0` + "\n",
		`gb_engine_mode{mode="SERVING"} 1` + "\n",
		`gb_turn_duration_seconds_bucket{kind="update",le="1"} 1` + "\n",
		`gb_turn_duration_seconds_bucket{kind="update",le="10"} 2` + "\n",
		`gb_turn_duration_seconds_bucket{kind="update",le="+Inf"} 3` + "\n",
		`gb_turn_duration_seconds_sum{kind="update"} 55.5` + "\n",
		`gb_turn_duration_seconds_count{kind="update"} 3` + "\n",
		"gb_panics_total 1\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Families are sorted by name and each HELP/TYPE pair appears once.
	if strings.Count(out, "# HELP gb_commands_total") != 1 {
		t.Error("duplicate family header")
	}
	if strings.Index(out, "gb_commands_total") > strings.Index(out, "gb_engine_mode") {
		t.Error("families not sorted")
	}
}

func TestEscapingAndGuards(t *testing.T) {
	r := NewRegistry()
	c := NewCounter(r, "x_total", "has \"quotes\"\nand newline", "l")
	c.Inc(`a"b\c` + "\n")
	var sb strings.Builder
	_, _ = r.WriteTo(&sb)
	if !strings.Contains(sb.String(), `x_total{l="a\"b\\c\n"} 1`) {
		t.Errorf("bad escaping: %s", sb.String())
	}
	if !strings.Contains(sb.String(), `# HELP x_total has "quotes"\nand newline`) {
		t.Errorf("bad help escaping: %s", sb.String())
	}
	mustPanic(t, func() { NewCounter(r, "x_total", "dup") })
	mustPanic(t, func() { c.Inc() })
	mustPanic(t, func() { c.Add(-1, "l") })
	mustPanic(t, func() { NewHistogram(r, "h", "", []float64{2, 1}) })
}

func mustPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	f()
}

// TestGaugeConcurrentAdd is the GB1-07 regression: 20 goroutines x 1,000
// increments must give exactly 20,000 (the old Add read and wrote under two
// separate lock holds and lost updates). Run with -race.
func TestGaugeConcurrentAdd(t *testing.T) {
	r := NewRegistry()
	g := NewGauge(r, "gb1_07_gauge", "Concurrent adds.")
	lg := NewGauge(r, "gb1_07_labelled", "Concurrent labelled adds.", "kind")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				g.Add(1)
				lg.Add(1, "a")
				lg.Add(-1, "b")
			}
		}()
	}
	wg.Wait()
	if got := g.Value(); got != 20000 {
		t.Fatalf("concurrent Add lost updates: got %v want 20000", got)
	}
	if a, b := lg.Value("a"), lg.Value("b"); a != 20000 || b != -20000 {
		t.Fatalf("labelled Add lost updates: a=%v b=%v", a, b)
	}
}

// TestGaugeConcurrentAddSetRead mixes Add, Set, Value and exposition: the
// race detector must stay quiet and the final value must be exactly what the
// last Set plus the Adds after it produce.
func TestGaugeConcurrentAddSetRead(t *testing.T) {
	r := NewRegistry()
	g := NewGauge(r, "gb1_07_mixed", "Mixed operations.")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = g.Value()
				var b strings.Builder
				_, _ = r.WriteTo(&b)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			g.Set(5)
		}
	}()
	var adders sync.WaitGroup
	for i := 0; i < 8; i++ {
		adders.Add(1)
		go func() {
			defer adders.Done()
			for j := 0; j < 500; j++ {
				g.Add(2)
			}
		}()
	}
	adders.Wait()
	close(stop)
	wg.Wait()
	g.Set(0)
	for i := 0; i < 8; i++ {
		adders.Add(1)
		go func() {
			defer adders.Done()
			for j := 0; j < 500; j++ {
				g.Add(2)
			}
		}()
	}
	adders.Wait()
	if got := g.Value(); got != 8000 {
		t.Fatalf("Add after Set: got %v want 8000", got)
	}
}
