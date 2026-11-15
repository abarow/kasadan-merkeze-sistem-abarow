// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natscashiers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/staff/internal/adapters/out/bcrypthasher"
	"abarow.com/services/core/staff/internal/adapters/out/postgres"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

func state(t *testing.T, id, name, role, pin string, disabled bool) []byte {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(&staffv1.CashierState{EventId: id + name, StoreId: "store-1", CashierId: id, Name: name, Role: role,
		PinHash: hash, Disabled: disabled, UpdatedBy: "mağaza"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecode(t *testing.T) {
	c, err := Decode("application/protobuf", state(t, "ayse", "Ayşe", "cashier", "2468", true))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "ayse" || c.Name != "Ayşe" || c.Role != domain.RoleCashier || !c.Disabled || c.Origin != domain.FromStore ||
		bcrypt.CompareHashAndPassword(c.PINHash, []byte("2468")) != nil {
		t.Errorf("cashier %+v", c)
	}
	if _, err := Decode("application/json", []byte("{}")); !errors.Is(err, errUndecodable) {
		t.Errorf("JSON: %v", err)
	}
	if _, err := Decode("application/protobuf", []byte{0xff}); !errors.Is(err, errUndecodable) {
		t.Errorf("broken message: %v", err)
	}
}

func startServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	opts.Host, opts.Port, opts.JetStream, opts.StoreDir, opts.NoLog, opts.NoSigs = "127.0.0.1", -1, true, t.TempDir(), true, true
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS did not start")
	}
	t.Cleanup(s.Shutdown)
	return s
}

func jsFor(t *testing.T, s *server.Server) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// The cashiers in the store server's store settings stream come down to the register over
// the leaf connection and are applied; the store prices do not enter the cashier stream.
func TestRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs NATS servers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hubOpts := &server.Options{ServerName: "store-1", JetStreamDomain: "store-1", LeafNode: server.LeafNodeOpts{Host: "127.0.0.1", Port: -1}}
	hub := startServer(t, hubOpts)
	leafURL, _ := url.Parse(fmt.Sprintf("nats://127.0.0.1:%d", hubOpts.LeafNode.Port))
	register := startServer(t, &server.Options{ServerName: "register-1", JetStreamDomain: "register-1",
		LeafNode: server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{URLs: []*url.URL{leafURL}}}}})
	deadline := time.Now().Add(10 * time.Second)
	for hub.NumLeafNodes() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("leaf did not connect")
		}
		time.Sleep(50 * time.Millisecond)
	}

	hubJS := jsFor(t, hub)
	if _, err := hubJS.CreateStream(ctx, jetstream.StreamConfig{Name: ConfigStream, Subjects: []string{"config.>"}, MaxMsgsPerSubject: 1}); err != nil {
		t.Fatal(err)
	}
	publish := func(subject string, data []byte) {
		msg := nats.NewMsg(subject)
		msg.Data = data
		msg.Header.Set("content-type", "application/protobuf")
		if _, err := hubJS.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	publish("config.cashier.ayse", state(t, "ayse", "Ayşe Yılmaz", "cashier", "2468", false))
	publish("config.cashier.veli", state(t, "veli", "Veli Kaya", "manager", "8642", false))
	publish("config.cashier.bozuk", []byte("bozuk"))
	publish("config.price.1", []byte("mağaza fiyatı"))

	st, err := postgres.Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auth := app.NewAuth(st, bcrypthasher.Hasher{Cost: bcrypt.MinCost}, time.Hour)
	registerJS := jsFor(t, register)
	go New(registerJS, auth, "store-1", slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)

	waitFor := func(what string, ok func([]domain.Cashier) bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			list, err := auth.List(ctx)
			if err == nil && ok(list) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v %v", what, list, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitFor("kasiyerlerin inmesi", func(l []domain.Cashier) bool { return len(l) == 2 })
	token, c, _, err := auth.Login(ctx, "ayse", "2468")
	if err != nil || c.Name != "Ayşe Yılmaz" {
		t.Fatalf("sign-in: %+v, %v", c, err)
	}

	// When the account is closed the open session closes.
	publish("config.cashier.ayse", state(t, "ayse", "Ayşe Yılmaz", "cashier", "2468", true))
	waitFor("hesabın kapanması", func(l []domain.Cashier) bool { return len(l) == 1 && l[0].ID == "veli" })
	if _, err := auth.Validate(ctx, token); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("session of the closed account: %v", err)
	}

	stream, err := registerJS.Stream(ctx, StreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx, jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := info.State.Subjects["config.price.1"]; ok || len(info.State.Subjects) != 3 {
		t.Errorf("subjects in the cashier stream: %v", info.State.Subjects)
	}
}
