// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/nodes"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func publicKey(t *testing.T, seedPath string) string {
	t.Helper()
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(readFile(t, seedPath))))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

func TestRun(t *testing.T) {
	out := t.TempDir()
	c := config{Role: "register", Out: out, Users: []string{"sales", "staff"}, ServerName: "register-1"}
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	conf := readFile(t, filepath.Join(out, "nats.conf"))
	pub := publicKey(t, filepath.Join(out, "sales", "nats.nk"))
	if !strings.Contains(conf, `nkey: "`+pub+`"`) || !strings.Contains(conf, `server_name: "register-1"`) || !strings.Contains(conf, `domain: "register-1"`) {
		t.Errorf("no sales key or register name in the configuration:\n%s", conf)
	}
	// Only sales can publish a sale event.
	if strings.Count(conf, `"sale.>"`) != 1 {
		t.Errorf("the sale.> publish permission should be once:\n%s", conf)
	}
	// Service keys can connect only as clients; no leaf.
	if strings.Count(conf, `allowed_connection_types: ["STANDARD"]`) != 2 || strings.Contains(conf, "leafnodes") {
		t.Errorf("connection types or leaf:\n%s", conf)
	}
	if strings.Contains(conf, "register-sim") {
		t.Error("an unwanted user in the configuration")
	}

	// Running again does not change the keys.
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(out, "nats.conf")) != conf {
		t.Error("the configuration changed on the second run")
	}

	for _, tt := range []struct {
		name string
		c    config
		want string
	}{
		{"bilinmeyen servis", config{Role: "register", Out: out, Users: []string{"retail"}, ServerName: "register-1"}, "bilinmeyen servis"},
		{"operasyon servisi kasada", config{Role: "register", Out: out, Users: []string{"report"}, ServerName: "register-1"}, "bilinmeyen servis"},
		{"bilinmeyen rol", config{Role: "yok-boyle-rol", Out: out, ServerName: "register-1"}, "bilinmeyen rol"},
		{"noktalı sunucu adı", config{Role: "register", Out: out, ServerName: "register.1"}, "server name"},
		{"kasada registers", config{Role: "register", Out: out, ServerName: "register-1", Registers: "r.json"}, "is not used in this role (register)"},
		{"kasada stores", config{Role: "register", Out: out, ServerName: "register-1", Stores: "s.json"}, "is not used in this role (register)"},
	} {
		if err := run(tt.c); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestAppSubjects(t *testing.T) {
	if got := appSubjects(); !slices.Equal(got, []string{"sale.>", "shift.>"}) {
		t.Errorf("application subjects %v", got)
	}
}

// storeTLS produces a store server's TLS files; it returns the folder.
func storeTLS(t *testing.T, hosts ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tls")
	if err := ensureLeafTLS(dir, hosts, "store-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegisterLeaf(t *testing.T) {
	out := t.TempDir()
	tlsDir := storeTLS(t, "store")
	c := config{Role: "register", Out: out, ServerName: "register-2", Leaf: "tls://store:7421", LeafCA: filepath.Join(tlsDir, caCertFile)}
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	conf := readFile(t, filepath.Join(out, "nats.conf"))
	seed := strings.TrimSpace(readFile(t, filepath.Join(out, "leaf", "nats.nk")))
	if readFile(t, filepath.Join(out, "leaf", caCertFile)) != readFile(t, c.LeafCA) {
		t.Error("the store server CA was not copied")
	}
	for _, want := range []string{
		`url: "tls://store:7421"`,
		`nkey: "` + seed + `"`,
		`tls { ca_file: "/auth/leaf/ca.pem" }`,
		`deny_imports: ["sale.>", "shift.>"]`,
		`deny_exports: ["sale.>", "shift.>"]`,
		`domain: "register-2"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("no %s in the configuration:\n%s", want, conf)
		}
	}
	// -show-leaf-key prints the same key; running again does not change it.
	pub, err := ensureKey(filepath.Join(out, "leaf", "nats.nk"))
	if err != nil || pub != publicKey(t, filepath.Join(out, "leaf", "nats.nk")) {
		t.Fatalf("leaf key: %v", err)
	}
	if err := run(c); err != nil || readFile(t, filepath.Join(out, "nats.conf")) != conf {
		t.Errorf("the configuration changed on the second run: %v", err)
	}

	// An address without TLS, a missing certificate or one that is not a CA is not accepted.
	for _, leaf := range []string{"nats://store:7422", "store:7422", "http://store:7422", "tls://register:parola@store:7422", "tls://"} {
		c.Leaf = leaf
		if err := run(c); err == nil || !strings.Contains(err.Error(), "leaf address") {
			t.Errorf("%s: %v", leaf, err)
		}
	}
	for _, tt := range []struct {
		name string
		c    config
		want string
	}{
		{"CA yok", config{Role: "register", Out: out, ServerName: "register-2", Leaf: "tls://store:7421"}, "-leaf-ca"},
		{"CA yerine sunucu sertifikası", config{Role: "register", Out: out, ServerName: "register-2", Leaf: "tls://store:7421", LeafCA: filepath.Join(tlsDir, serverCertFile)}, "is not a CA certificate"},
		{"CA dosyası yok", config{Role: "register", Out: out, ServerName: "register-2", Leaf: "tls://store:7421", LeafCA: filepath.Join(tlsDir, "yok.pem")}, "could not read"},
		{"leaf olmadan CA", config{Role: "register", Out: out, ServerName: "register-2", LeafCA: c.LeafCA}, "is used only with -leaf"},
	} {
		if err := run(tt.c); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestLeafTLS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Now()
	if err := ensureLeafTLS(dir, []string{"store", "192.168.1.10"}, "store-1", now); err != nil {
		t.Fatal(err)
	}
	ca, err := readCert(filepath.Join(dir, caCertFile))
	if err != nil || !ca.IsCA {
		t.Fatalf("CA: %v", err)
	}
	server, err := readCert(filepath.Join(dir, serverCertFile))
	if err != nil {
		t.Fatal(err)
	}
	if server.CheckSignatureFrom(ca) != nil || !slices.Equal(server.DNSNames, []string{"store"}) || len(server.IPAddresses) != 1 || server.IPAddresses[0].String() != "192.168.1.10" {
		t.Errorf("server certificate: %v %v %v", server.CheckSignatureFrom(ca), server.DNSNames, server.IPAddresses)
	}
	if err := server.VerifyHostname("192.168.1.10"); err != nil {
		t.Error(err)
	}

	// Running again with the same addresses changes nothing.
	caPEM, serverPEM := readFile(t, filepath.Join(dir, caCertFile)), readFile(t, filepath.Join(dir, serverCertFile))
	if err := ensureLeafTLS(dir, []string{"192.168.1.10", "store"}, "store-1", now); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, caCertFile)) != caPEM || readFile(t, filepath.Join(dir, serverCertFile)) != serverPEM {
		t.Error("the certificate changed with the same addresses")
	}
	// When the addresses change the server certificate is renewed, the CA stays the same (the
	// registers are not affected).
	if err := ensureLeafTLS(dir, []string{"store", "192.168.1.20"}, "store-1", now); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, caCertFile)) != caPEM || readFile(t, filepath.Join(dir, serverCertFile)) == serverPEM {
		t.Error("when the addresses change only the server certificate should be renewed")
	}
	// It is renewed when it is about to expire.
	serverPEM = readFile(t, filepath.Join(dir, serverCertFile))
	if err := ensureLeafTLS(dir, []string{"store", "192.168.1.20"}, "store-1", now.Add(serverValidity-renewBefore/2)); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, serverCertFile)) == serverPEM {
		t.Error("a certificate about to expire was not renewed")
	}

	// If the CA's private key is lost a new CA is not produced: the CA at the registers
	// would become invalid.
	if err := os.Remove(filepath.Join(dir, caKeyFile)); err != nil {
		t.Fatal(err)
	}
	if err := ensureLeafTLS(dir, []string{"store"}, "store-1", now); err == nil || !strings.Contains(err.Error(), "CA files are missing") {
		t.Errorf("missing CA key: %v", err)
	}
	if err := ensureLeafTLS(t.TempDir(), nil, "store-1", now); err == nil || !strings.Contains(err.Error(), "-leaf-hosts") {
		t.Errorf("no address: %v", err)
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	regs := filepath.Join(dir, "registers.json")
	out := filepath.Join(dir, "auth")
	keys := map[string]string{}
	for _, id := range []string{"register-1", "register-2"} {
		kp, _ := nkeys.CreateUser()
		keys[id], _ = kp.PublicKey()
		if err := addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: id, NKey: keys[id]}); err != nil {
			t.Fatal(err)
		}
	}
	c := config{Role: "store", Out: out, Users: []string{"report", "register-sim"}, ServerName: "store-1", Registers: regs, LeafHosts: []string{"store"}}
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	conf := readFile(t, filepath.Join(out, "nats.conf"))
	for _, want := range []string{
		"port: 7421",
		`cert_file: "/auth/tls/server.pem"`,
		`key_file: "/auth/tls/server.key"`,
		`domain: "store-1"`,
		`nkey: "` + keys["register-1"] + `"`,
		`nkey: "` + keys["register-2"] + `"`,
		`"$JS.register-1.API.CONSUMER.CREATE.ABAROW_SALES"`,
		`"$JS.register-2.API.CONSUMER.CREATE.ABAROW_SALES"`,
		`"$JS.register-2.API.CONSUMER.CREATE.ABAROW_STAFF"`,
		`"$JS.API.STREAM.CREATE.ABAROW_STORE"`,
		`"$JS.ACK.ABAROW_STORE.report.>"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("no %s in the configuration:\n%s", want, conf)
		}
	}
	// The CA certificate to be given to the registers is in a folder separate from the private keys.
	if readFile(t, filepath.Join(out, "ca", caCertFile)) != readFile(t, filepath.Join(out, "tls", caCertFile)) {
		t.Error("there is no ca/ca.pem or it differs from tls/ca.pem")
	}
	if entries, _ := os.ReadDir(filepath.Join(out, "ca")); len(entries) != 1 {
		t.Errorf("the ca folder should contain only the CA certificate: %v", entries)
	}
	if strings.Count(conf, `allowed_connection_types: ["LEAFNODE"]`) != 2 || strings.Count(conf, `allowed_connection_types: ["STANDARD"]`) != 2 {
		t.Errorf("connection types:\n%s", conf)
	}
	// A register cannot reach the application subjects; in the store server's JetStream API
	// it can open a source consumer only for the catalog, store settings and receipt
	// streams.
	if strings.Contains(conf, `"sale.>"`) {
		t.Errorf("kasaya fazla yetki:\n%s", conf)
	}
	allowed := func(field string) bool {
		for _, s := range []string{"ABAROW_CATALOG", "ABAROW_STORE_CONFIG", "ABAROW_RECEIPT"} {
			for _, op := range []string{"CREATE." + s, "CREATE." + s + ".", "DELETE." + s + "."} {
				if field == "$JS.store-1.API.CONSUMER."+op || (strings.HasSuffix(op, ".") && strings.HasPrefix(field, "$JS.store-1.API.CONSUMER."+op)) {
					return true
				}
			}
		}
		return false
	}
	for _, field := range strings.FieldsFunc(conf, func(r rune) bool { return strings.ContainsRune(" \t\n[],\"", r) }) {
		if strings.HasPrefix(field, "$JS.store-1.") && !allowed(field) {
			t.Errorf("kasaya operasyon API'sinde fazla yetki: %s", field)
		}
	}
	if !strings.Contains(conf, `"$JS.store-1.API.CONSUMER.CREATE.ABAROW_CATALOG.>"`) {
		t.Errorf("a register cannot get the catalog:\n%s", conf)
	}

	// When a register is removed it cannot connect.
	if err := removeNode(regs, "-registers", "operasyon NATS'ı", "register-1"); err != nil {
		t.Fatal(err)
	}
	if err := run(c); err != nil {
		t.Fatal(err)
	}
	if conf := readFile(t, filepath.Join(out, "nats.conf")); strings.Contains(conf, keys["register-1"]) || !strings.Contains(conf, keys["register-2"]) {
		t.Errorf("the removed register is in the configuration:\n%s", conf)
	}

	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"registers yok", run(config{Role: "store", Out: out, ServerName: "store-1", LeafHosts: c.LeafHosts}), "-registers"},
		{"operasyonda CA'sız leaf", run(config{Role: "store", Out: out, ServerName: "store-1", Registers: regs, LeafHosts: c.LeafHosts, Leaf: "tls://x:7422"}), "-leaf-ca"},
		{"operasyonda stores", run(config{Role: "store", Out: out, ServerName: "store-1", Registers: regs, Stores: regs, LeafHosts: c.LeafHosts}), "is not used in this role (store)"},
		{"kasa servisi operasyonda", run(config{Role: "store", Out: out, Users: []string{"sales"}, ServerName: "store-1", Registers: regs, LeafHosts: c.LeafHosts}), "bilinmeyen servis"},
		{"kasa adı operasyon adıyla aynı", run(config{Role: "store", Out: out, ServerName: "register-2", Registers: regs, LeafHosts: c.LeafHosts}), "cannot be the same"},
		{"leaf adresi yok", run(config{Role: "store", Out: out, ServerName: "store-1", Registers: regs}), "-leaf-hosts"},
		{"geçersiz anahtar", addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: "register-3", NKey: "UABC"}), "the key is invalid"},
		{"gizli anahtar", addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: "register-3", NKey: "SUAPYE4CDXQGYIQHUZWK6HZZ5CQDG2WZ7RTYQ6N2XOWPD5HOB5ZFJOECLM"}), "the key is invalid"},
		{"aynı anahtar iki kasada", addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: "register-3", NKey: keys["register-2"]}), "on another device"},
		{"listede olmayan kasa", removeNode(regs, "-registers", "operasyon NATS'ı", "register-9"), "is not in the list"},
		{"registers yolu yok", addNode("", "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: "register-3", NKey: keys["register-1"]}), "-registers"},
	} {
		if tt.err == nil || !strings.Contains(tt.err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, tt.err)
		}
	}
}

// startServer starts an in-process NATS server with the configuration natsauth wrote;
// the ports are random, the data is in a temporary folder.
func startServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Shutdown)
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS did not start")
	}
	return s
}

func serverOptions(t *testing.T, confPath string) *server.Options {
	t.Helper()
	opts, err := server.ProcessConfigFile(confPath)
	if err != nil {
		t.Fatalf("the generated configuration is invalid in NATS: %v", err)
	}
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.HTTPPort = 0
	opts.StoreDir = t.TempDir()
	opts.NoSigs = true
	opts.NoLog = true
	return opts
}

func connect(t *testing.T, s *server.Server, opts ...nats.Option) (*nats.Conn, error) {
	t.Helper()
	nc, err := nats.Connect(s.ClientURL(), append(opts, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))...)
	if err == nil {
		t.Cleanup(nc.Close)
	}
	return nc, err
}

func jsAs(t *testing.T, s *server.Server, seedPath string) jetstream.JetStream {
	t.Helper()
	opt, err := nats.NkeyOptionFromSeed(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := connect(t, s, opt)
	if err != nil {
		t.Fatalf("%s: %v", seedPath, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func msgCount(t *testing.T, js jetstream.JetStream, stream string) uint64 {
	t.Helper()
	s, err := js.Stream(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

func waitFor(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: did not happen within %s", what, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLeafSourcing runs a store server and two registers in-process with the generated
// configurations: the sale events reach the store server without loss or
// repetition, one register's event does not fall into another register's stream, the register
// key is used at the store server only as leaf and with narrow permission;
// the events that accumulate while the store server is down come once it opens.
func TestLeafSourcing(t *testing.T) {
	if testing.Short() {
		t.Skip("runs NATS servers (about a minute)")
	}
	ctx := context.Background()
	dir := t.TempDir()
	regs := filepath.Join(dir, "registers.json")
	registerIDs := []string{"register-1", "register-2"}
	// The file paths in the configuration point to the test folder (/auth in the container).
	store := filepath.Join(dir, "store-1")
	bhConfig := config{Role: "store", Out: store, Users: []string{"report", "register-sim"}, ServerName: "store-1", Registers: regs, LeafHosts: []string{"127.0.0.1"}, ConfDir: filepath.ToSlash(store)}
	if err := run(bhConfig); err != nil {
		t.Fatal(err)
	}
	for _, id := range registerIDs {
		out := filepath.Join(dir, id)
		c := config{Role: "register", Out: out, Users: []string{"sales", "staff", "register-sim"}, ServerName: id, Leaf: "tls://127.0.0.1:7422", LeafCA: filepath.Join(store, "ca", caCertFile), ConfDir: filepath.ToSlash(out)}
		if err := run(c); err != nil {
			t.Fatal(err)
		}
		if err := addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: id, NKey: publicKey(t, filepath.Join(out, "leaf", "nats.nk"))}); err != nil {
			t.Fatal(err)
		}
	}
	// A registered register that trusts another CA: it cannot verify the store server's
	// certificate, it does not connect (it would not connect to a fake store server either).
	wrongCA := filepath.Join(dir, "register-3")
	if err := run(config{Role: "register", Out: wrongCA, Users: []string{"sales"}, ServerName: "register-3", Leaf: "tls://127.0.0.1:7422", LeafCA: filepath.Join(storeTLS(t, "127.0.0.1"), caCertFile), ConfDir: filepath.ToSlash(wrongCA)}); err != nil {
		t.Fatal(err)
	}
	if err := addNode(regs, "-registers", "operasyon NATS'ı ve report", nodes.Node{ID: "register-3", NKey: publicKey(t, filepath.Join(wrongCA, "leaf", "nats.nk"))}); err != nil {
		t.Fatal(err)
	}
	// After the registers are added the store server configuration is regenerated.
	if err := run(bhConfig); err != nil {
		t.Fatal(err)
	}

	hubOpts := serverOptions(t, filepath.Join(store, "nats.conf"))
	hubOpts.LeafNode.Host = "127.0.0.1"
	hubOpts.LeafNode.Port = -1
	hub := startServer(t, hubOpts)
	leafURL, _ := url.Parse(fmt.Sprintf("tls://127.0.0.1:%d", hubOpts.LeafNode.Port))

	register := map[string]*server.Server{}
	for _, id := range registerIDs {
		opts := serverOptions(t, filepath.Join(dir, id, "nats.conf"))
		if len(opts.LeafNode.Remotes) != 1 {
			t.Fatalf("%s: no leaf connection", id)
		}
		opts.LeafNode.Remotes[0].URLs = []*url.URL{leafURL}
		if id == "register-1" {
			// A user with permission for everything at the register: to try the permission
			// the store server gave to the register connection.
			opts.Users = append(opts.Users, &server.User{Username: "test", Password: "test"})
		}
		register[id] = startServer(t, opts)
	}
	waitFor(t, 10*time.Second, "kasaların leaf bağlantısı", func() bool { return hub.NumLeafNodes() == 2 })
	opts := serverOptions(t, filepath.Join(wrongCA, "nats.conf"))
	opts.LeafNode.Remotes[0].URLs = []*url.URL{leafURL}
	startServer(t, opts)
	time.Sleep(3 * time.Second)
	if n := hub.NumLeafNodes(); n != 2 {
		t.Errorf("a register that trusts another CA connected (%d leaf)", n)
	}

	// Sets up the sales stream at the registers and publishes a sale event.
	published := map[string]int{"register-1": 3, "register-2": 2}
	sales := map[string]jetstream.JetStream{}
	for _, id := range registerIDs {
		sales[id] = jsAs(t, register[id], filepath.Join(dir, id, "sales", "nats.nk"))
		if _, err := sales[id].CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: salesStream, Subjects: []string{"sale.>"}, Storage: jetstream.FileStorage}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for i := range published[id] {
			if _, err := sales[id].Publish(ctx, "sale.completed.v1", []byte(fmt.Sprintf("%s sale %d", id, i)), jetstream.WithMsgID(fmt.Sprintf("%s-%d", id, i))); err != nil {
				t.Fatal(err)
			}
		}
		// staff sets up the shift stream and publishes a shift event.
		staff := jsAs(t, register[id], filepath.Join(dir, id, "staff", "nats.nk"))
		if _, err := staff.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: staffStream, Subjects: []string{"shift.>"}, Storage: jetstream.FileStorage}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if _, err := staff.Publish(ctx, "shift.opened.v1", []byte(id+" vardiya"), jetstream.WithMsgID(id+"-vardiya")); err != nil {
			t.Fatal(err)
		}
	}

	// report sets up the store stream from the registers' sales and shift streams.
	report := jsAs(t, hub, filepath.Join(store, "report", "nats.nk"))
	var sources []*jetstream.StreamSource
	for _, id := range registerIDs {
		sources = append(sources, &jetstream.StreamSource{Name: salesStream, Domain: id}, &jetstream.StreamSource{Name: staffStream, Domain: id})
	}
	if _, err := report.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: storeStream, Storage: jetstream.FileStorage, Sources: sources}); err != nil {
		t.Fatal(err)
	}
	// 5 sales + 1 shift event from each register.
	waitFor(t, 30*time.Second, "olayların operasyona gelmesi", func() bool { return msgCount(t, report, storeStream) == 7 })

	// One register's event does not fall into another register's stream.
	for _, id := range registerIDs {
		sim := jsAs(t, register[id], filepath.Join(dir, id, "register-sim", "nats.nk"))
		if n := msgCount(t, sim, salesStream); n != uint64(published[id]) {
			t.Errorf("the %s stream has %d events; only its own %d events should be there", id, n, published[id])
		}
	}

	// The register key cannot connect to the store server as a client.
	opt, _ := nats.NkeyOptionFromSeed(filepath.Join(dir, "register-1", "leaf", "nats.nk"))
	if _, err := connect(t, hub, opt, nats.MaxReconnects(0)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "authorization") {
		t.Errorf("client connection with the register key: %v", err)
	}
	// Even a user with permission for everything at the register cannot reach the store server's
	// JetStream API over the leaf (if it were allowed a reply would have come).
	nc, err := connect(t, register["register-1"], nats.UserInfo("test", "test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nc.Request("$JS.store-1.API.INFO", nil, time.Second); err == nil {
		t.Error("the register reached the store server's JetStream API")
	}

	// While the store server is down the register keeps selling; when it opens the accumulated events
	// come. Catching up can take tens of seconds because of the source retry interval.
	hub.Shutdown()
	hub.WaitForShutdown()
	for i := range 2 {
		if _, err := sales["register-1"].Publish(ctx, "sale.completed.v1", []byte("while closed"), jetstream.WithMsgID(fmt.Sprintf("kapali-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	hub = startServer(t, hubOpts)
	report = jsAs(t, hub, filepath.Join(store, "report", "nats.nk"))
	waitFor(t, 90*time.Second, "operasyon açılınca biriken olaylar", func() bool { return msgCount(t, report, storeStream) == 9 })
	time.Sleep(2 * time.Second)
	if n := msgCount(t, report, storeStream); n != 9 {
		t.Errorf("%d events at the store server; should be 9 (there is a repeat)", n)
	}
}

// TestThreeTiers runs the register → store server → headquarters chain in-process:
// two stores, in each a register with the same name (register-1; each store's first
// register is named this way). The sales reach headquarters without loss, the stores
// do not get mixed up, the store server key connects to headquarters only as
// leaf.
func TestThreeTiers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs NATS servers")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conf := func(name string) string { return filepath.ToSlash(filepath.Join(dir, name)) }
	stores := filepath.Join(dir, "stores.json")
	headquartersConfig := config{Role: "headquarters", Out: filepath.Join(dir, "headquarters"), Users: []string{"ingest", "catalog", "register-sim"}, ServerName: "headquarters",
		Stores: stores, LeafHosts: []string{"127.0.0.1"}, ConfDir: conf("headquarters")}
	if err := run(headquartersConfig); err != nil {
		t.Fatal(err)
	}
	// Headquarters listens for stores on 7422, the store server for registers on 7421.
	if c := readFile(t, filepath.Join(dir, "headquarters", "nats.conf")); !strings.Contains(c, "port: 7422") {
		t.Errorf("headquarters' leaf port is not 7422: %s", c)
	}

	storeIDs := []string{"store-1", "store-2"}
	published := map[string]int{"store-1": 3, "store-2": 2}
	for _, s := range storeIDs {
		regs := filepath.Join(dir, s+"-registers.json")
		store := config{Role: "store", Out: filepath.Join(dir, s), Users: []string{"report"}, ServerName: s, Registers: regs,
			LeafHosts: []string{"127.0.0.1"}, Leaf: "tls://127.0.0.1:7422", LeafCA: filepath.Join(dir, "headquarters", "ca", caCertFile), ConfDir: conf(s)}
		if err := run(store); err != nil {
			t.Fatal(err)
		}
		k := config{Role: "register", Out: filepath.Join(dir, s+"-register-1"), Users: []string{"sales", "staff", "catalog", "register-sim"}, ServerName: "register-1",
			Leaf: "tls://127.0.0.1:7422", LeafCA: filepath.Join(dir, s, "ca", caCertFile), ConfDir: conf(s + "-register-1")}
		if err := run(k); err != nil {
			t.Fatal(err)
		}
		if err := addNode(regs, "-registers", "store", nodes.Node{ID: "register-1", NKey: publicKey(t, filepath.Join(k.Out, "leaf", "nats.nk"))}); err != nil {
			t.Fatal(err)
		}
		if err := run(store); err != nil {
			t.Fatal(err)
		}
		if err := addNode(stores, "-stores", "headquarters", nodes.Node{ID: s, NKey: publicKey(t, filepath.Join(store.Out, "leaf", "nats.nk"))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(headquartersConfig); err != nil {
		t.Fatal(err)
	}
	if c := readFile(t, filepath.Join(dir, "headquarters", "nats.conf")); !strings.Contains(c, `"$JS.store-1.API.CONSUMER.CREATE.ABAROW_STORE"`) || strings.Contains(c, "ABAROW_SALES") {
		t.Errorf("store permissions at headquarters:\n%s", c)
	}

	// Servers: headquarters, the stores (connected to headquarters), the registers.
	headquartersOpts := serverOptions(t, filepath.Join(dir, "headquarters", "nats.conf"))
	headquartersOpts.LeafNode.Host, headquartersOpts.LeafNode.Port = "127.0.0.1", -1
	headquarters := startServer(t, headquartersOpts)
	headquartersURL, _ := url.Parse(fmt.Sprintf("tls://127.0.0.1:%d", headquartersOpts.LeafNode.Port))
	storeServers := map[string]*server.Server{}
	registerServers := map[string]*server.Server{}
	for _, s := range storeIDs {
		opts := serverOptions(t, filepath.Join(dir, s, "nats.conf"))
		opts.LeafNode.Host, opts.LeafNode.Port = "127.0.0.1", -1
		if len(opts.LeafNode.Remotes) != 1 {
			t.Fatalf("%s: no headquarters connection", s)
		}
		opts.LeafNode.Remotes[0].URLs = []*url.URL{headquartersURL}
		storeServers[s] = startServer(t, opts)
		bhURL, _ := url.Parse(fmt.Sprintf("tls://127.0.0.1:%d", opts.LeafNode.Port))
		kopts := serverOptions(t, filepath.Join(dir, s+"-register-1", "nats.conf"))
		kopts.LeafNode.Remotes[0].URLs = []*url.URL{bhURL}
		registerServers[s] = startServer(t, kopts)
	}
	waitFor(t, 10*time.Second, "leaf bağlantıları", func() bool {
		ok := headquarters.NumLeafNodes() == 2
		for _, store := range storeServers {
			ok = ok && store.NumLeafNodes() == 2 // register (down) + headquarters (up)
		}
		return ok
	})

	// Sales at the registers; the store stream at the store server; the headquarters stream at headquarters.
	for _, s := range storeIDs {
		sales := jsAs(t, registerServers[s], filepath.Join(dir, s+"-register-1", "sales", "nats.nk"))
		if _, err := sales.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: salesStream, Subjects: []string{"sale.>"}, Storage: jetstream.FileStorage}); err != nil {
			t.Fatal(err)
		}
		for i := range published[s] {
			if _, err := sales.Publish(ctx, "sale.completed.v1", []byte(s), jetstream.WithMsgID(fmt.Sprintf("%s-%d", s, i))); err != nil {
				t.Fatal(err)
			}
		}
		report := jsAs(t, storeServers[s], filepath.Join(dir, s, "report", "nats.nk"))
		if _, err := report.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: storeStream, Storage: jetstream.FileStorage,
			Sources: []*jetstream.StreamSource{{Name: salesStream, Domain: "register-1"}}}); err != nil {
			t.Fatal(err)
		}
	}
	ingest := jsAs(t, headquarters, filepath.Join(dir, "headquarters", "ingest", "nats.nk"))
	var sources []*jetstream.StreamSource
	for _, s := range storeIDs {
		// Like ingest: the store id is added to the subject; which store an event came from is
		// determined by headquarters, not by the event's content.
		sources = append(sources, &jetstream.StreamSource{Name: storeStream, Domain: s,
			SubjectTransforms: []jetstream.SubjectTransformConfig{{Source: ">", Destination: s + ".>"}}})
	}
	if _, err := ingest.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: headquartersStream, Storage: jetstream.FileStorage, Sources: sources}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "satışların merkeze gelmesi", func() bool { return msgCount(t, ingest, headquartersStream) == 5 })
	time.Sleep(2 * time.Second)
	if n := msgCount(t, ingest, headquartersStream); n != 5 {
		t.Errorf("%d events at headquarters; should be 5", n)
	}
	// ingest sets up its own durable consumer and can delete it (to read the stream
	// from the start when restoring from a backup); it cannot set up another consumer.
	if _, err := ingest.CreateOrUpdateConsumer(ctx, headquartersStream, jetstream.ConsumerConfig{Durable: "ingest", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatalf("ingest consumer: %v", err)
	}
	if err := ingest.DeleteConsumer(ctx, headquartersStream, "ingest"); err != nil {
		t.Errorf("could not delete the ingest consumer: %v", err)
	}
	if _, err := ingest.CreateOrUpdateConsumer(ctx, headquartersStream, jetstream.ConsumerConfig{Durable: "baska", AckPolicy: jetstream.AckExplicitPolicy}); err == nil {
		t.Error("ingest could build another consumer")
	}
	// Registers with the same name do not get mixed up: each store gets only its own register's sales.
	headquartersInfo, err := ingest.Stream(ctx, headquartersStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := headquartersInfo.Info(ctx, jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range storeIDs {
		if n := info.State.Subjects[s+".sale.completed.v1"]; n != uint64(published[s]) {
			t.Errorf("the %s subject at headquarters has %d events; should be %d (%v)", s, n, published[s], info.State.Subjects)
		}
	}
	for _, s := range storeIDs {
		report := jsAs(t, storeServers[s], filepath.Join(dir, s, "report", "nats.nk"))
		if n := msgCount(t, report, storeStream); n != uint64(published[s]) {
			t.Errorf("the %s store stream has %d events; should be %d", s, n, published[s])
		}
	}
	// The store server key cannot connect to headquarters as a client.
	opt, _ := nats.NkeyOptionFromSeed(filepath.Join(dir, "store-1", "leaf", "nats.nk"))
	if _, err := connect(t, headquarters, opt, nats.MaxReconnects(0)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "authorization") {
		t.Errorf("client connection with the store server key: %v", err)
	}

	// Downward flow: headquarters' catalog goes down to the store servers and from there to the registers.
	// In every tier only the latest state per product is kept.
	catalogConfig := func(sources ...*jetstream.StreamSource) jetstream.StreamConfig {
		cfg := jetstream.StreamConfig{Name: catalogStream, Storage: jetstream.FileStorage, MaxMsgsPerSubject: 1, Sources: sources}
		if len(sources) == 0 {
			cfg.Subjects = []string{"catalog.product.>"}
		}
		return cfg
	}
	catalog := jsAs(t, headquarters, filepath.Join(dir, "headquarters", "catalog", "nats.nk"))
	if _, err := catalog.CreateOrUpdateStream(ctx, catalogConfig()); err != nil {
		t.Fatal(err)
	}
	for _, m := range [][2]string{{"ekmek", "v1"}, {"sut", "v1"}, {"ekmek", "v2"}} {
		if _, err := catalog.Publish(ctx, "catalog.product."+m[0], []byte(m[1])); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range storeIDs {
		report := jsAs(t, storeServers[s], filepath.Join(dir, s, "report", "nats.nk"))
		if _, err := report.CreateOrUpdateStream(ctx, catalogConfig(&jetstream.StreamSource{Name: catalogStream, Domain: "headquarters"})); err != nil {
			t.Fatalf("%s: the store's catalog stream: %v", s, err)
		}
		registerCatalog := jsAs(t, registerServers[s], filepath.Join(dir, s+"-register-1", "catalog", "nats.nk"))
		if _, err := registerCatalog.CreateOrUpdateStream(ctx, catalogConfig(&jetstream.StreamSource{Name: catalogStream, Domain: s})); err != nil {
			t.Fatalf("%s: the register's catalog stream: %v", s, err)
		}
	}
	for _, s := range storeIDs {
		registerCatalog := jsAs(t, registerServers[s], filepath.Join(dir, s+"-register-1", "catalog", "nats.nk"))
		waitFor(t, 60*time.Second, s+" kasasına katalog inmesi", func() bool { return msgCount(t, registerCatalog, catalogStream) == 2 })
		// Like the catalog service at the register: it reads with its own durable consumer.
		cons, err := registerCatalog.CreateOrUpdateConsumer(ctx, catalogStream, jetstream.ConsumerConfig{Durable: "catalog", AckPolicy: jetstream.AckExplicitPolicy})
		if err != nil {
			t.Fatal(err)
		}
		batch, err := cons.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for msg := range batch.Messages() {
			got[msg.Subject()] = string(msg.Data())
			_ = msg.Ack()
		}
		if got["catalog.product.ekmek"] != "v2" || got["catalog.product.sut"] != "v1" {
			t.Errorf("catalog at register %s %v", s, got)
		}
	}
	// Store settings: the store server's cashiers go down to the staff stream at the register;
	// the store prices do not enter that stream.
	report := jsAs(t, storeServers["store-1"], filepath.Join(dir, "store-1", "report", "nats.nk"))
	if _, err := report.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: configStream, Subjects: []string{"config.>"}, Storage: jetstream.FileStorage, MaxMsgsPerSubject: 1}); err != nil {
		t.Fatal(err)
	}
	for _, m := range [][2]string{{"config.cashier.ayse", "v1"}, {"config.price.ekmek", "price"}, {"config.cashier.ayse", "v2"}, {"config.cashier.ali", "v1"}} {
		if _, err := report.Publish(ctx, m[0], []byte(m[1])); err != nil {
			t.Fatal(err)
		}
	}
	staff := jsAs(t, registerServers["store-1"], filepath.Join(dir, "store-1-register-1", "staff", "nats.nk"))
	if _, err := staff.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: cashiersStream, Storage: jetstream.FileStorage, MaxMsgsPerSubject: 1,
		Sources: []*jetstream.StreamSource{{Name: configStream, Domain: "store-1", FilterSubject: "config.cashier.>"}}}); err != nil {
		t.Fatalf("the register's cashier stream: %v", err)
	}
	waitFor(t, 60*time.Second, "kasaya kasiyerlerin inmesi", func() bool { return msgCount(t, staff, cashiersStream) == 2 })
	cons, err := staff.CreateOrUpdateConsumer(ctx, cashiersStream, jetstream.ConsumerConfig{Durable: "staff", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cashiers := map[string]string{}
	for msg := range batch.Messages() {
		cashiers[msg.Subject()] = string(msg.Data())
		_ = msg.Ack()
	}
	if len(cashiers) != 2 || cashiers["config.cashier.ayse"] != "v2" || cashiers["config.cashier.ali"] != "v1" {
		t.Errorf("kasadaki kasiyerler %v", cashiers)
	}

	// A register can take only the catalog and store settings from the store server:
	// it cannot open the store stream (all the registers' sales) as a source.
	sim := jsAs(t, registerServers["store-1"], filepath.Join(dir, "store-1-register-1", "catalog", "nats.nk"))
	if _, err := sim.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: "ABAROW_STORE", Sources: []*jetstream.StreamSource{{Name: storeStream, Domain: "store-1"}}}); err == nil {
		t.Error("the register's catalog user could build another stream")
	}
	// Even if the catalog stream is pointed at the store stream the leaf does not allow it:
	// the sales do not come down to the register.
	if _, err := sim.CreateOrUpdateStream(ctx, catalogConfig(&jetstream.StreamSource{Name: storeStream, Domain: "store-1"})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(8 * time.Second)
	if n := msgCount(t, sim, catalogStream); n != 2 {
		t.Errorf("%d events came down to the register from the store stream", n-2)
	}

	// Delivery receipt (pkg/delivery): headquarters → store server → register. A receipt goes down only to
	// its own owner; an event whose receipt is recorded is deleted, one without stays.
	if _, err := ingest.CreateOrUpdateStream(ctx, delivery.ReceiptConfig("", "", 0)); err != nil {
		t.Fatalf("ingest makbuz stream'i: %v", err)
	}
	for _, s := range storeIDs {
		b, _ := delivery.Receipt{EventID: s + "-0", ReceiptID: "headquarters-" + s, At: time.Now()}.Marshal()
		if _, err := ingest.Publish(ctx, delivery.Subject(s, storeStream), b); err != nil {
			t.Fatalf("ingest receipt publishing: %v", err)
		}
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// receiptBook is a tier's receipt record (in reality deliverydb.Store).
	type receiptBook struct {
		mu    sync.Mutex
		saved map[string]delivery.Receipt
	}
	newBook := func() *receiptBook { return &receiptBook{saved: map[string]delivery.Receipt{}} }
	save := func(b *receiptBook) func(context.Context, delivery.Receipt) error {
		return func(_ context.Context, r delivery.Receipt) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.saved[r.EventID] = r
			return nil
		}
	}
	received := func(b *receiptBook) func(context.Context, []string) (map[string]bool, error) {
		return func(_ context.Context, ids []string) (map[string]bool, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			out := map[string]bool{}
			for _, id := range ids {
				_, ok := b.saved[id]
				out[id] = ok
			}
			return out, nil
		}
	}
	for _, s := range storeIDs {
		s := s
		report := jsAs(t, storeServers[s], filepath.Join(dir, s, "report", "nats.nk"))
		if _, err := report.CreateOrUpdateStream(ctx, delivery.ReceiptConfig("headquarters", s, 0)); err != nil {
			t.Fatalf("%s makbuz stream'i: %v", s, err)
		}
		// The store server gives the register a receipt (publishes locally).
		b, _ := delivery.Receipt{EventID: s + "-0", ReceiptID: "store-" + s, At: time.Now()}.Marshal()
		if _, err := report.Publish(ctx, delivery.Subject("register-1", salesStream), b); err != nil {
			t.Fatalf("%s receipt publishing: %v", s, err)
		}
		// Headquarters' receipt goes down to the store (its own + the one given to the register = 2); another store's does not come.
		waitFor(t, 60*time.Second, s+" makbuzlarının gelmesi", func() bool { return msgCount(t, report, delivery.ReceiptStream) == 2 })
		time.Sleep(2 * time.Second)
		if n := msgCount(t, report, delivery.ReceiptStream); n != 2 {
			t.Errorf("the %s receipt stream has %d messages; should be 2 (another store's receipt may have come)", s, n)
		}

		// Store server: records headquarters' receipt, deletes the event with a receipt in the store stream.
		bhBook := newBook()
		go delivery.NewReceiptConsumer(report, "receipts", delivery.Subject(s, storeStream), save(bhBook), log).Run(runCtx)
		waitFor(t, 30*time.Second, s+" makbuzunun kaydı", func() bool { bhBook.mu.Lock(); defer bhBook.mu.Unlock(); return len(bhBook.saved) == 1 })
		store, err := report.Stream(ctx, storeStream)
		if err != nil {
			t.Fatal(err)
		}
		limit, has, err := delivery.LocalFloor(ctx, store)
		if err != nil {
			t.Fatalf("%s: report could not look at the consumers: %v", s, err)
		}
		if has {
			t.Errorf("%s: there is a limit (%d) while there is no named consumer in the store stream", s, limit)
		}
		bhClean := &delivery.Cleaner{Log: delivery.JSLog{S: store}, Received: received(bhBook)}
		if n, err := bhClean.Clean(ctx, 0, time.Now().Add(time.Hour)); err != nil || n != 1 {
			t.Fatalf("%s: store server cleanup deleted=%d err=%v; 1 expected", s, n, err)
		}
		if n := msgCount(t, report, storeStream); n != uint64(published[s]-1) {
			t.Errorf("the %s store stream has %d events; an event with a receipt should be deleted, leaving %d", s, n, published[s]-1)
		}

		// Register: gets only its own receipt, deletes the event with a receipt.
		sales := jsAs(t, registerServers[s], filepath.Join(dir, s+"-register-1", "sales", "nats.nk"))
		if _, err := sales.CreateOrUpdateStream(ctx, delivery.ReceiptConfig(s, "register-1", 0)); err != nil {
			t.Fatalf("%s kasa makbuz stream'i: %v", s, err)
		}
		waitFor(t, 60*time.Second, s+" kasasına makbuz inmesi", func() bool { return msgCount(t, sales, delivery.ReceiptStream) == 1 })
		time.Sleep(2 * time.Second)
		if n := msgCount(t, sales, delivery.ReceiptStream); n != 1 {
			t.Errorf("the %s register receipt stream has %d messages; only its own receipt (1) should be there", s, n)
		}
		registerBook := newBook()
		go delivery.NewReceiptConsumer(sales, "sales-receipts", delivery.Subject("register-1", salesStream), save(registerBook), log).Run(runCtx)
		waitFor(t, 30*time.Second, s+" kasa makbuzunun kaydı", func() bool {
			registerBook.mu.Lock()
			defer registerBook.mu.Unlock()
			return len(registerBook.saved) == 1
		})
		if r := registerBook.saved[s+"-0"]; r.ReceiptID != "store-"+s {
			t.Errorf("%s register receipt %+v; should be the store server's receipt", s, r)
		}
		registerSales, err := sales.Stream(ctx, salesStream)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := delivery.LocalFloor(ctx, registerSales); err != nil {
			t.Fatalf("%s: sales could not look at the consumers: %v", s, err)
		}
		registerClean := &delivery.Cleaner{Log: delivery.JSLog{S: registerSales}, Received: received(registerBook)}
		if n, err := registerClean.Clean(ctx, 0, time.Now().Add(time.Hour)); err != nil || n != 1 {
			t.Fatalf("%s: register cleanup deleted=%d err=%v; 1 expected", s, n, err)
		}
		if n := msgCount(t, sales, salesStream); n != uint64(published[s]-1) {
			t.Errorf("the %s register sales stream has %d events; %d should remain", s, n, published[s]-1)
		}
	}
}
