// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/nodes"
)

// freePort returns a port nobody listens on (the cluster ports must be known before the
// servers start: every server gets the addresses of the others).
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// copyTo copies the files headquarters' first machine gives to the others (keys, the CA).
func copyTo(t *testing.T, from, to string, files ...string) {
	t.Helper()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(from, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, f), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClusterConfig(t *testing.T) {
	dir := t.TempDir()
	routes := []string{"nats://hq1.example.com:6222", "nats://hq2.example.com:6222", "nats://hq3.example.com:6222"}
	c := config{Role: "headquarters", Out: filepath.Join(dir, "hq"), Users: []string{"ingest"}, ServerName: "headquarters-2", Domain: "headquarters",
		Stores: filepath.Join(dir, "stores.json"), LeafHosts: []string{"hq2.example.com"}, Routes: routes, ConfDir: "/etc/nats"}
	if err := addNode(c.Stores, "-stores", "headquarters", nodes.Node{ID: "store-1", NKey: storeKey(t)}); err != nil {
		t.Fatal(err)
	}
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	conf := readFile(t, filepath.Join(c.Out, "nats.conf"))
	for _, want := range []string{
		`server_name: "headquarters-2"`,
		`domain: "headquarters"`,
		`name: "headquarters"`,
		"port: 6222",
		`routes: ["nats://hq1.example.com:6222", "nats://hq2.example.com:6222", "nats://hq3.example.com:6222"]`,
		`ca_file: "/etc/nats/tls/ca.pem"`,
		"verify: true",
		// The stores reach headquarters' streams by the shared domain, not by the machine's name.
		`"$JS.headquarters.API.CONSUMER.CREATE.ABAROW_CATALOG"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("no %s in the configuration:\n%s", want, conf)
		}
	}
	// Neither the routes nor the leaf port tell the container's address (docs/REVIEW-2.md M5).
	if n := strings.Count(conf, "no_advertise: true"); n != 2 {
		t.Errorf("no_advertise %d times, want 2 (cluster and leafnodes):\n%s", n, conf)
	}
	if strings.Contains(conf, "$JS.headquarters-2.") {
		t.Errorf("the machine's name is used as the domain:\n%s", conf)
	}
	// The store knows the three machines; with one address the configuration stays as before.
	storeOut := filepath.Join(dir, "store")
	sc := config{Role: "store", Out: storeOut, ServerName: "store-1", Registers: filepath.Join(dir, "registers.json"), LeafHosts: []string{"store"},
		Leaf: "tls://hq1.example.com:7422, tls://hq2.example.com:7422,tls://hq3.example.com:7422", LeafCA: filepath.Join(c.Out, "ca", caCertFile)}
	if err := run(sc); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, filepath.Join(storeOut, "nats.conf")); !strings.Contains(s, `urls: ["tls://hq1.example.com:7422", "tls://hq2.example.com:7422", "tls://hq3.example.com:7422"]`) {
		t.Errorf("the store does not know the three machines:\n%s", s)
	}
	sc.Leaf = "tls://hq1.example.com:7422,nats://hq2.example.com:7422"
	if err := run(sc); err == nil || !strings.Contains(err.Error(), "leaf address") {
		t.Errorf("an address without TLS among the three: %v", err)
	}

	cert, err := readCert(filepath.Join(c.Out, "tls", serverCertFile))
	if err != nil {
		t.Fatal(err)
	}
	if !sameHosts(cert, []string{"hq2.example.com"}) {
		t.Errorf("certificate names %v", cert.DNSNames)
	}

	for _, tt := range []struct {
		name string
		c    func(c config) config
		want string
	}{
		{"two machines", func(c config) config { c.Routes = routes[:2]; return c }, "at least three machines"},
		{"no shared domain", func(c config) config { c.Domain = ""; return c }, "shared -domain"},
		{"route without port", func(c config) config { c.Routes = []string{"nats://a", "nats://b:6222", "nats://c:6222"}; return c }, "cluster address"},
		{"route with TLS scheme", func(c config) config { c.Routes = []string{"tls://a:6222", "nats://b:6222", "nats://c:6222"}; return c }, "cluster address"},
		{"route with identity", func(c config) config {
			c.Routes = []string{"nats://u:p@a:6222", "nats://b:6222", "nats://c:6222"}
			return c
		}, "cluster address"},
		{"store with routes", func(c config) config {
			return config{Role: "store", Out: filepath.Join(dir, "s"), ServerName: "store-1", Domain: "x", Registers: filepath.Join(dir, "r.json"), LeafHosts: []string{"s"}, Routes: routes}
		}, "only at headquarters"},
		{"invalid domain", func(c config) config { c.Domain = "head.quarters"; return c }, "domain"},
		{"store named like the domain", func(c config) config { c.ServerName = "headquarters-9"; c.Domain = "store-1"; return c }, "server's name or domain"},
	} {
		if err := run(tt.c(c)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func storeKey(t *testing.T) string {
	t.Helper()
	pub, err := ensureKey(filepath.Join(t.TempDir(), "nats.nk"))
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// TestHeadquartersCluster: headquarters on three machines, a store connected to all three. While
// events flow from the store, the machine leading the headquarters stream is shut down; the
// events keep coming, none is lost or repeated.
func TestHeadquartersCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("runs NATS servers")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conf := func(name string) string { return filepath.ToSlash(filepath.Join(dir, name)) }
	stores := filepath.Join(dir, "stores.json")
	ports := []int{freePort(t), freePort(t), freePort(t)}
	var routes []string
	for _, p := range ports {
		routes = append(routes, fmt.Sprintf("nats://127.0.0.1:%d", p))
	}
	hq := func(i int) config {
		name := fmt.Sprintf("hq-%d", i+1)
		return config{Role: "headquarters", Out: filepath.Join(dir, name), Users: []string{"ingest", "register-sim"}, ServerName: fmt.Sprintf("headquarters-%d", i+1),
			Domain: "headquarters", Stores: stores, LeafHosts: []string{"127.0.0.1"}, Routes: routes, ConfDir: conf(name)}
	}

	// The first machine produces the keys and the CA; the store trusts that CA.
	if err := run(hq(0)); err != nil {
		t.Fatal(err)
	}
	store := config{Role: "store", Out: filepath.Join(dir, "store-1"), Users: []string{"report"}, ServerName: "store-1", Registers: filepath.Join(dir, "registers.json"),
		LeafHosts: []string{"127.0.0.1"}, Leaf: "tls://127.0.0.1:7422", LeafCA: filepath.Join(dir, "hq-1", "ca", caCertFile), ConfDir: conf("store-1")}
	if err := run(store); err != nil {
		t.Fatal(err)
	}
	if err := addNode(stores, "-stores", "headquarters", nodes.Node{ID: "store-1", NKey: publicKey(t, filepath.Join(store.Out, "leaf", "nats.nk"))}); err != nil {
		t.Fatal(err)
	}
	// The other machines get the keys and the CA of the first one; each makes its own certificate.
	for i := range 3 {
		if i > 0 {
			copyTo(t, filepath.Join(dir, "hq-1"), filepath.Join(dir, fmt.Sprintf("hq-%d", i+1)),
				"tls/ca.pem", "tls/ca.key", "ingest/nats.nk", "register-sim/nats.nk")
		}
		if err := run(hq(i)); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := readFile(t, filepath.Join(dir, "hq-1", "ingest", "nats.nk")), readFile(t, filepath.Join(dir, "hq-3", "ingest", "nats.nk")); a != b {
		t.Fatal("the machines have different ingest keys")
	}

	servers := make([]*server.Server, 3)
	var leafURLs []*url.URL
	for i := range 3 {
		opts := serverOptions(t, filepath.Join(dir, fmt.Sprintf("hq-%d", i+1), "nats.conf"))
		opts.LeafNode.Host, opts.LeafNode.Port = "127.0.0.1", -1
		opts.Cluster.Host, opts.Cluster.Port = "127.0.0.1", ports[i]
		if len(opts.Routes) != 3 || opts.Cluster.TLSConfig == nil {
			t.Fatalf("headquarters-%d: no cluster in the configuration", i+1)
		}
		servers[i] = startServer(t, opts)
		u, _ := url.Parse(fmt.Sprintf("tls://127.0.0.1:%d", opts.LeafNode.Port))
		leafURLs = append(leafURLs, u)
	}
	waitFor(t, 30*time.Second, "küme", func() bool {
		for _, s := range servers {
			if s.NumRoutes() < 2 || !s.JetStreamIsClustered() {
				return false
			}
		}
		return servers[0].JetStreamIsCurrent()
	})

	opts := serverOptions(t, filepath.Join(store.Out, "nats.conf"))
	opts.LeafNode.Host, opts.LeafNode.Port = "127.0.0.1", -1
	opts.LeafNode.Remotes[0].URLs = leafURLs
	storeServer := startServer(t, opts)
	waitFor(t, 15*time.Second, "mağazanın merkeze bağlanması", func() bool { return storeServer.NumLeafNodes() == 1 })

	report := jsAs(t, storeServer, filepath.Join(store.Out, "report", "nats.nk"))
	if _, err := report.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: storeStream, Subjects: []string{"store.>"}, Storage: jetstream.FileStorage}); err != nil {
		t.Fatal(err)
	}
	publish := func(from, to int) {
		t.Helper()
		for i := from; i < to; i++ {
			if _, err := report.Publish(ctx, "store.stock.moved.v1", []byte(fmt.Sprint(i)), jetstream.WithMsgID(fmt.Sprint(i))); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish(0, 5)

	// ingest sets up the headquarters stream with three copies.
	ingest := jsAs(t, servers[0], filepath.Join(dir, "hq-1", "ingest", "nats.nk"))
	cfg := jetstream.StreamConfig{Name: headquartersStream, Storage: jetstream.FileStorage, Replicas: 3,
		Sources: []*jetstream.StreamSource{{Name: storeStream, Domain: "store-1",
			SubjectTransforms: []jetstream.SubjectTransformConfig{{Source: ">", Destination: "store-1.>"}}}}}
	// The cluster may still be choosing its leader: setting up is retried like ingest does
	// (delivery.EnsureStream).
	var createErr error
	waitFor(t, 30*time.Second, "üç kopyalı stream", func() bool {
		_, createErr = ingest.CreateOrUpdateStream(ctx, cfg)
		return createErr == nil
	})
	waitFor(t, 60*time.Second, "olayların merkeze gelmesi", func() bool { return msgCount(t, ingest, headquartersStream) == 5 })

	// The machine leading the stream is shut down.
	stream, err := ingest.Stream(ctx, headquartersStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Cluster == nil || len(info.Cluster.Replicas) != 2 {
		t.Fatalf("the stream does not have three copies: %+v", info.Cluster)
	}
	down := -1
	for i, s := range servers {
		if s.Name() == info.Cluster.Leader {
			down = i
		}
	}
	if down < 0 {
		t.Fatalf("leader %q is not one of the machines", info.Cluster.Leader)
	}
	servers[down].Shutdown()
	alive := servers[(down+1)%3]
	ingest = jsAs(t, alive, filepath.Join(dir, fmt.Sprintf("hq-%d", (down+1)%3+1), "ingest", "nats.nk"))

	publish(5, 10)
	waitFor(t, 60*time.Second, "makine kapandıktan sonra olayların gelmesi", func() bool {
		s, err := ingest.Stream(ctx, headquartersStream)
		if err != nil {
			return false
		}
		info, err := s.Info(ctx)
		return err == nil && info.State.Msgs == 10
	})
	time.Sleep(2 * time.Second)
	if n := msgCount(t, ingest, headquartersStream); n != 10 {
		t.Errorf("%d events at headquarters; should be 10 (none lost or repeated)", n)
	}
	if storeServer.NumLeafNodes() != 1 {
		t.Error("the store is not connected to another machine")
	}
}
