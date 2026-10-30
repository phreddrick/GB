// Package metrics is a small, dependency-free Prometheus text-exposition
// registry for the Phase 1 server. It supports counters, gauges and
// histograms with a bounded, declared label set per metric. Label values are
// supplied by the server code only (never by players or sessions), which
// keeps cardinality bounded by construction.
//
// The format follows the Prometheus text format 0.0.4 (one HELP and TYPE line
// per family, samples sorted by label values), which is what
// client_golang emits and what Prometheus and VictoriaMetrics scrape.
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Registry holds metric families. The zero value is not usable; use NewRegistry.
type Registry struct {
	mu       sync.Mutex
	families []family
	byName   map[string]family
}

type family interface {
	name() string
	write(w io.Writer)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]family{}}
}

func (r *Registry) register(f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byName[f.name()]; dup {
		panic("metrics: duplicate metric " + f.name())
	}
	r.byName[f.name()] = f
	r.families = append(r.families, f)
	sort.Slice(r.families, func(i, j int) bool { return r.families[i].name() < r.families[j].name() })
}

// WriteTo renders every family in text exposition format.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	r.mu.Lock()
	fams := append([]family(nil), r.families...)
	r.mu.Unlock()
	cw := &countingWriter{w: w}
	for _, f := range fams {
		f.write(cw)
	}
	return cw.n, cw.err
}

type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
	return n, err
}

// labelKey joins label values with a separator that cannot appear after
// escaping; it is used as the map key for a labelled child.
func labelKey(values []string) string { return strings.Join(values, "\x00") }

func formatLabels(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escape(values[i]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func writeHeader(w io.Writer, name, help, typ string) {
	_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, typ)
}

func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

// --- Counter ---

// Counter is a monotonically increasing family of float64 samples.
type Counter struct {
	fname, help string
	labels      []string
	mu          sync.Mutex
	children    map[string]*counterChild
}

type counterChild struct {
	values []string
	v      float64
}

// NewCounter registers a counter family with the given label names.
func NewCounter(r *Registry, name, help string, labels ...string) *Counter {
	c := &Counter{fname: name, help: help, labels: labels, children: map[string]*counterChild{}}
	r.register(c)
	if len(labels) == 0 {
		c.children[""] = &counterChild{}
	}
	return c
}

func (c *Counter) name() string { return c.fname }

// Inc adds one to the sample identified by the label values.
func (c *Counter) Inc(values ...string) { c.Add(1, values...) }

// Add adds v (which must not be negative) to the sample.
func (c *Counter) Add(v float64, values ...string) {
	if v < 0 {
		panic("metrics: counter decreased")
	}
	if len(values) != len(c.labels) {
		panic("metrics: wrong label count for " + c.fname)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	k := labelKey(values)
	ch, ok := c.children[k]
	if !ok {
		ch = &counterChild{values: append([]string(nil), values...)}
		c.children[k] = ch
	}
	ch.v += v
}

// Value returns the current sample (for tests).
func (c *Counter) Value(values ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.children[labelKey(values)]; ok {
		return ch.v
	}
	return 0
}

func (c *Counter) write(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	writeHeader(w, c.fname, c.help, "counter")
	for _, k := range sortedKeys(c.children) {
		ch := c.children[k]
		_, _ = fmt.Fprintf(w, "%s%s %s\n", c.fname, formatLabels(c.labels, ch.values), formatFloat(ch.v))
	}
}

// --- Gauge ---

// Gauge is a family of float64 samples that can go up and down.
type Gauge struct {
	fname, help string
	labels      []string
	mu          sync.Mutex
	children    map[string]*gaugeChild
}

type gaugeChild struct {
	values []string
	v      float64
}

// NewGauge registers a gauge family.
func NewGauge(r *Registry, name, help string, labels ...string) *Gauge {
	g := &Gauge{fname: name, help: help, labels: labels, children: map[string]*gaugeChild{}}
	r.register(g)
	if len(labels) == 0 {
		g.children[""] = &gaugeChild{}
	}
	return g
}

func (g *Gauge) name() string { return g.fname }

// Set sets the sample identified by the label values.
func (g *Gauge) Set(v float64, values ...string) {
	g.update(values, func(float64) float64 { return v })
}

// Add adds v to the sample. The read, the addition and the write happen
// under one hold of the family lock, so concurrent Add, Set and Value calls
// never lose an update (GB1-07).
func (g *Gauge) Add(v float64, values ...string) {
	g.update(values, func(old float64) float64 { return old + v })
}

// update applies f to the sample identified by values under g.mu.
func (g *Gauge) update(values []string, f func(old float64) float64) {
	if len(values) != len(g.labels) {
		panic("metrics: wrong label count for " + g.fname)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := labelKey(values)
	ch, ok := g.children[k]
	if !ok {
		ch = &gaugeChild{values: append([]string(nil), values...)}
		g.children[k] = ch
	}
	ch.v = f(ch.v)
}

// Value returns the current sample (for tests).
func (g *Gauge) Value(values ...string) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ch, ok := g.children[labelKey(values)]; ok {
		return ch.v
	}
	return 0
}

func (g *Gauge) write(w io.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeHeader(w, g.fname, g.help, "gauge")
	for _, k := range sortedKeys(g.children) {
		ch := g.children[k]
		_, _ = fmt.Fprintf(w, "%s%s %s\n", g.fname, formatLabels(g.labels, ch.values), formatFloat(ch.v))
	}
}

// --- Histogram ---

// Histogram is a family of cumulative-bucket histograms.
type Histogram struct {
	fname, help string
	labels      []string
	buckets     []float64 // upper bounds, ascending, without +Inf
	mu          sync.Mutex
	children    map[string]*histChild
}

type histChild struct {
	values []string
	counts []uint64 // per bucket, non-cumulative
	sum    float64
	count  uint64
}

// DefaultDurationBuckets suit turn and command durations in seconds.
var DefaultDurationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600}

// NewHistogram registers a histogram family with the given bucket upper
// bounds (ascending).
func NewHistogram(r *Registry, name, help string, buckets []float64, labels ...string) *Histogram {
	if !sort.Float64sAreSorted(buckets) {
		panic("metrics: histogram buckets must be ascending")
	}
	h := &Histogram{fname: name, help: help, labels: labels, buckets: append([]float64(nil), buckets...), children: map[string]*histChild{}}
	r.register(h)
	return h
}

func (h *Histogram) name() string { return h.fname }

// Observe records one value.
func (h *Histogram) Observe(v float64, values ...string) {
	if len(values) != len(h.labels) {
		panic("metrics: wrong label count for " + h.fname)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	k := labelKey(values)
	ch, ok := h.children[k]
	if !ok {
		ch = &histChild{values: append([]string(nil), values...), counts: make([]uint64, len(h.buckets))}
		h.children[k] = ch
	}
	for i, ub := range h.buckets {
		if v <= ub {
			ch.counts[i]++
			break
		}
	}
	ch.sum += v
	ch.count++
}

// Count returns the number of observations for the label values (for tests).
func (h *Histogram) Count(values ...string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.children[labelKey(values)]; ok {
		return ch.count
	}
	return 0
}

func (h *Histogram) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	writeHeader(w, h.fname, h.help, "histogram")
	leNames := append(append([]string(nil), h.labels...), "le")
	for _, k := range sortedKeys(h.children) {
		ch := h.children[k]
		var cum uint64
		for i, ub := range h.buckets {
			cum += ch.counts[i]
			_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", h.fname, formatLabels(leNames, append(append([]string(nil), ch.values...), formatFloat(ub))), cum)
		}
		_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", h.fname, formatLabels(leNames, append(append([]string(nil), ch.values...), "+Inf")), ch.count)
		_, _ = fmt.Fprintf(w, "%s_sum%s %s\n", h.fname, formatLabels(h.labels, ch.values), formatFloat(ch.sum))
		_, _ = fmt.Fprintf(w, "%s_count%s %d\n", h.fname, formatLabels(h.labels, ch.values), ch.count)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
