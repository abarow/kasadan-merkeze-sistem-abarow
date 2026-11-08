// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWrite(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("t_requests_total", "Requests.", "route", "code")
	c.Inc("GET /a", "200")
	c.Inc("GET /a", "200")
	c.Inc(`GET /"b"`, "500")
	h := r.Histogram("t_seconds", "Durations.", "route")
	h.Observe(30*time.Millisecond, "GET /a")
	h.Observe(2*time.Second, "GET /a")
	r.Gauge("t_gauge", "A value.", func() float64 { return 7 })
	// The same name gives the same counter.
	r.Counter("t_requests_total", "Requests.", "route", "code").Inc("GET /a", "200")

	var b bytes.Buffer
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		"# TYPE t_requests_total counter\n",
		`t_requests_total{route="GET /a",code="200"} 3` + "\n",
		`t_requests_total{route="GET /\"b\"",code="500"} 1` + "\n",
		"# TYPE t_seconds histogram\n",
		`t_seconds_bucket{route="GET /a",le="0.025"} 0` + "\n",
		`t_seconds_bucket{route="GET /a",le="0.05"} 1` + "\n",
		`t_seconds_bucket{route="GET /a",le="2.5"} 2` + "\n",
		`t_seconds_bucket{route="GET /a",le="+Inf"} 2` + "\n",
		`t_seconds_sum{route="GET /a"} 2.03` + "\n",
		`t_seconds_count{route="GET /a"} 2` + "\n",
		"# TYPE t_gauge gauge\nt_gauge 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDefaultHasRuntime(t *testing.T) {
	var b bytes.Buffer
	Default.Write(&b)
	if !strings.Contains(b.String(), "go_goroutines ") || !strings.Contains(b.String(), "go_memstats_heap_inuse_bytes ") {
		t.Errorf("runtime values missing:\n%s", b.String())
	}
}
