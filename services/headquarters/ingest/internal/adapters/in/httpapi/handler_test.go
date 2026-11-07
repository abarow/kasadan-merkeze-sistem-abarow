// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"abarow.com/pkg/nodes"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

type stream struct {
	sources map[string]domain.Source
	err     error
}

func (s stream) Ensure(context.Context, []string) error { return nil }
func (s stream) Sources(context.Context) (map[string]domain.Source, error) {
	return s.sources, s.err
}

type store struct{ activity map[string]domain.Activity }

func (store) Record(context.Context, domain.Event, any) (bool, error) { return true, nil }
func (s store) Activity(context.Context) (map[string]domain.Activity, error) {
	return s.activity, nil
}
func (store) SaveLinks(context.Context, []domain.StoreStatus, time.Time) error { return nil }

func TestStores(t *testing.T) {
	last := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	svc := app.NewService(
		stream{sources: map[string]domain.Source{"store-1": {Seen: true, Since: time.Second, Lag: 3}}},
		store{activity: map[string]domain.Activity{"store-1": {Events: 12, LastEventAt: last, Sales: 6,
			Registers: []domain.RegisterActivity{{ID: "register-1", Events: 8, Sales: 4, LastEventAt: last}, {ID: "register-2", Events: 4, Sales: 2}}}}},
		[]nodes.Node{{ID: "store-1", Name: "Kadıköy"}, {ID: "store-2"}})
	rec := httptest.NewRecorder()
	New(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stores", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got []storeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Kadıköy" || got[0].Link != "connected" || got[0].PendingEvents != 3 || got[0].EventCount != 12 ||
		got[0].SaleCount != 6 || got[0].LastEventAt == nil || !got[0].LastEventAt.Equal(last) ||
		len(got[0].Registers) != 2 || got[0].Registers[0] != (registerJSON{ID: "register-1", EventCount: 8, SaleCount: 4, LastEventAt: last}) ||
		got[1].Name != "store-2" || got[1].Link != "never" || got[1].LastSeen != nil || got[1].LastEventAt != nil || got[1].Registers == nil {
		t.Errorf("stores %+v", got)
	}

	rec = httptest.NewRecorder()
	New(app.NewService(stream{err: errors.New("nats: timeout")}, store{}, nil)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stores", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("NATS error: %d", rec.Code)
	}
}
