// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

// open sets up an empty database for each test (ABAROW_TEST_POSTGRES; the test is skipped if it is not set).
func open(t testing.TB) *Store {
	t.Helper()
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("ABAROW_TEST_POSTGRES yok; PostgreSQL testleri atlandı")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abarow_update_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	store, err := Open(ctx, stdlib.RegisterConnConfig(cfg))
	if err != nil {
		admin.ExecContext(ctx, "DROP DATABASE "+name)
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return store
}

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func request(device string, tier token.Tier) registration.Request {
	r := registration.Request{
		Device: device, Public: []byte("0123456789abcdef0123456789abcdef"), Fingerprint: "fp", Tier: tier, Level: token.Pilot,
		Store: "store-1", Register: "register-1", Name: "Kasa 1", Status: registration.Pending, CreatedAt: t0,
	}
	if tier == token.Headquarters {
		r.SigningKey = []byte("fedcba9876543210fedcba9876543210")
	}
	return r
}

func TestCreateGetList(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	if _, err := s.Get(ctx, "yok"); !errors.Is(err, registration.ErrNotFound) {
		t.Errorf("olmayan kayıt: %v", err)
	}
	a := request("ABRW-A", token.Register)
	b := request("ABRW-B", token.Headquarters)
	b.CreatedAt = t0.Add(time.Minute)
	for _, r := range []registration.Request{a, b} {
		if err := s.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, a); !errors.Is(err, registration.ErrExists) {
		t.Errorf("yinelenen kayıt: %v", err)
	}
	got, err := s.Get(ctx, "ABRW-B")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != token.Headquarters || string(got.SigningKey) != string(b.SigningKey) || !got.CreatedAt.Equal(b.CreatedAt) || !got.DecidedAt.IsZero() || got.Level != token.Pilot {
		t.Errorf("okunan kayıt: %+v", got)
	}
	if g, _ := s.Get(ctx, "ABRW-A"); len(g.SigningKey) != 0 {
		t.Errorf("merkez dışında imzalama anahtarı var: %x", g.SigningKey)
	}
	list, err := s.List(ctx, registration.Pending)
	if err != nil || len(list) != 2 || list[0].Device != "ABRW-A" || list[1].Device != "ABRW-B" {
		t.Errorf("liste: %+v, %v", list, err)
	}
	if other, _ := s.List(ctx, registration.Approved); len(other) != 0 {
		t.Errorf("onaylı liste: %+v", other)
	}
}

func TestUpdateIsCompareAndSwap(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	r := request("ABRW-A", token.Register)
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	approval := r
	approval.Status, approval.DecidedAt, approval.DecidedBy = registration.Approved, t0.Add(time.Hour), "yonetici"
	if err := s.Update(ctx, r, approval); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "ABRW-A")
	if got.Status != registration.Approved || got.DecidedBy != "yonetici" || !got.DecidedAt.Equal(approval.DecidedAt) {
		t.Errorf("güncel kayıt: %+v", got)
	}
	// The second update with the old view conflicts and the first decision is not overwritten.
	revoked := r
	revoked.Status = registration.Revoked
	if err := s.Update(ctx, r, revoked); !errors.Is(err, registration.ErrConflict) {
		t.Errorf("eski görüntüyle güncelleme: %v", err)
	}
	if got, _ := s.Get(ctx, "ABRW-A"); got.Status != registration.Approved {
		t.Errorf("çakışan güncelleme kaydı ezdi: %s", got.Status)
	}
	if err := s.Update(ctx, request("ABRW-YOK", token.Register), approval); !errors.Is(err, registration.ErrNotFound) {
		t.Errorf("olmayan kaydı güncelleme: %v", err)
	}
}

// In twenty concurrent wrong-code attempts the counter rises only by the number of successful writes and does not go back.
func TestConcurrentAttemptsLoseNoCounterUpdate(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	r := request("ABRW-A", token.Register)
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	var ok atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := r
			next.Attempts = r.Attempts + 1
			if err := s.Update(ctx, r, next); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	got, _ := s.Get(ctx, "ABRW-A")
	if ok.Load() != 1 || got.Attempts != 1 {
		t.Errorf("aynı eski görüntüyle %d güncelleme başarılı, sayaç %d; yalnızca biri geçmeliydi", ok.Load(), got.Attempts)
	}
}

// The full flow of the registration service works with the PostgreSQL store.
func TestServiceOverPostgres(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	svc, err := registration.New(registration.Config{
		Store: s, Issuer: registration.SigningIssuer{Key: rootPriv, ID: token.PublisherIssuer},
		Allowed: []token.Tier{token.Register}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	i, err := identity.Generate(identity.NewFingerprint(map[string]string{"machine-id": "pg"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	in := registration.Submission{Device: i.ID, Public: i.Public, Fingerprint: string(i.Fingerprint), Tier: token.Register, Level: token.Prod, Store: "m", Register: "k", Name: "Kasa"}
	in.Signature = i.Sign(registration.SubmitMessage(in.Device, in.Tier, in.Level, in.Name, nil))
	if _, err := svc.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, in); err != nil { // a repeat is harmless
		t.Fatal(err)
	}
	if err := svc.Approve(ctx, "yonetici", i.ID, identity.Code(i.ID)); err != nil {
		t.Fatal(err)
	}
	nonce, err := svc.Challenge(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Token(ctx, i.ID, nonce, i.Sign(token.PossessionMessage(i.ID, nonce)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := token.Verify(rootPub, res.Token, time.Now())
	if err != nil || c.Device != i.ID || c.Tier != token.Register {
		t.Fatalf("jeton: %+v, %v", c, err)
	}
	if err := svc.Revoke(ctx, "yonetici", i.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Challenge(ctx, i.ID); !errors.Is(err, registration.ErrRevoked) {
		t.Errorf("iptal sonrası: %v", err)
	}
}
