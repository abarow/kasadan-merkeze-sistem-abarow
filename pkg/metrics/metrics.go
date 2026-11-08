// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package metrics keeps a service's counters and duration histograms and writes them in
// the Prometheus text format (the /metrics endpoint of the health port, pkg/service).
// It is a small writer instead of the Prometheus client library: the register runs ten
// services in little memory and needs only counters, histograms and a few values read
// at scrape time.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Buckets are the upper bounds of the duration histograms in seconds: from a quick
// database read to a monthly report.
var Buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// Registry holds the metrics of a service.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]*family
	histograms map[string]*family
	gauges     []gauge
}

type gauge struct {
	name, help string
	read       func() float64
}

// family is the series of one metric, by their label values.
type family struct {
	name, help string
	labels     []string
	series     map[string]*series
}

type series struct {
	values []string
	count  float64   // a counter's value, a histogram's count
	sum    float64   // a histogram's sum
	bucket []float64 // a histogram's cumulative counts
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{counters: map[string]*family{}, histograms: map[string]*family{}}
}

// Default is the registry of the service (the health port serves it).
var Default = NewRegistry()

// Counter is a counter with labels.
type Counter struct {
	r *Registry
	f *family
}

// Histogram is a duration histogram with labels (Buckets).
type Histogram struct {
	r *Registry
	f *family
}

// Counter returns the counter of that name (the same one if it exists).
func (r *Registry) Counter(name, help string, labels ...string) Counter {
	return Counter{r, r.family(r.counters, name, help, labels)}
}

// Histogram returns the histogram of that name (the same one if it exists).
func (r *Registry) Histogram(name, help string, labels ...string) Histogram {
	return Histogram{r, r.family(r.histograms, name, help, labels)}
}

// Gauge adds a value read at every scrape (for example the number of goroutines).
func (r *Registry) Gauge(name, help string, read func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, gauge{name, help, read})
}

func (r *Registry) family(m map[string]*family, name, help string, labels []string) *family {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := m[name]; ok {
		return f
	}
	f := &family{name: name, help: help, labels: labels, series: map[string]*series{}}
	m[name] = f
	return f
}

func (f *family) get(values []string, buckets int) *series {
	key := strings.Join(values, "\xff")
	s, ok := f.series[key]
	if !ok {
		s = &series{values: slices.Clone(values), bucket: make([]float64, buckets)}
		f.series[key] = s
	}
	return s
}

// Inc adds one; the label values are in the order of the labels.
func (c Counter) Inc(values ...string) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	c.f.get(values, 0).count++
}

// Observe records a duration.
func (h Histogram) Observe(d time.Duration, values ...string) {
	v := d.Seconds()
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	s := h.f.get(values, len(Buckets))
	s.count++
	s.sum += v
	for i, b := range Buckets {
		if v <= b {
			s.bucket[i]++
		}
	}
}

// Handler serves the registry in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.Write(w)
	})
}

// Write writes the registry in the Prometheus text format, the names in order.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range sorted(r.counters) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", f.name, f.help, f.name)
		for _, s := range f.sortedSeries() {
			fmt.Fprintf(w, "%s%s %s\n", f.name, labels(f.labels, s.values, "", ""), num(s.count))
		}
	}
	for _, f := range sorted(r.histograms) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", f.name, f.help, f.name)
		for _, s := range f.sortedSeries() {
			for i, b := range Buckets {
				fmt.Fprintf(w, "%s_bucket%s %s\n", f.name, labels(f.labels, s.values, "le", num(b)), num(s.bucket[i]))
			}
			fmt.Fprintf(w, "%s_bucket%s %s\n", f.name, labels(f.labels, s.values, "le", "+Inf"), num(s.count))
			fmt.Fprintf(w, "%s_sum%s %s\n", f.name, labels(f.labels, s.values, "", ""), num(s.sum))
			fmt.Fprintf(w, "%s_count%s %s\n", f.name, labels(f.labels, s.values, "", ""), num(s.count))
		}
	}
	gs := slices.Clone(r.gauges)
	sort.Slice(gs, func(i, j int) bool { return gs[i].name < gs[j].name })
	for _, g := range gs {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", g.name, g.help, g.name, g.name, num(g.read()))
	}
}

func sorted(m map[string]*family) []*family {
	out := make([]*family, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (f *family) sortedSeries() []*series {
	out := make([]*series, 0, len(f.series))
	for _, s := range f.series {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Join(out[i].values, "\xff") < strings.Join(out[j].values, "\xff") })
	return out
}

// labels writes {name="value",...}; extra is one more label (le of a bucket).
func labels(names, values []string, extra, extraValue string) string {
	var b strings.Builder
	for i, n := range names {
		v := ""
		if i < len(values) {
			v = values[i]
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n + `="` + escape(v) + `"`)
	}
	if extra != "" {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extra + `="` + extraValue + `"`)
	}
	if b.Len() == 0 {
		return ""
	}
	return "{" + b.String() + "}"
}

func escape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func num(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Runtime adds the Go runtime values to the registry: goroutines, heap in use and the
// total memory taken from the system.
func Runtime(r *Registry) {
	r.Gauge("go_goroutines", "Number of goroutines.", func() float64 { return float64(runtime.NumGoroutine()) })
	r.Gauge("go_memstats_heap_inuse_bytes", "Bytes in in-use heap spans.", func() float64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.HeapInuse)
	})
	r.Gauge("go_memstats_sys_bytes", "Bytes of memory obtained from the OS.", func() float64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.Sys)
	})
}

func init() { Runtime(Default) }
