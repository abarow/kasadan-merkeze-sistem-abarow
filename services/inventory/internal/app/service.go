// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the use cases of the stock service: deducting a sale from stock, goods
// receipt, counting, transfer, the stock list and a product's movements. The same service runs
// in two scopes (Config):
//
//   - store (the store server): a single place, the store itself. Sales are
//     deducted from stock, every movement goes to headquarters as an event, transfers come
//     down from headquarters. The storage is SQLite.
//   - headquarters (Headquarters): the headquarters warehouse and all stores. The stores' movements
//     are processed, a store-to-store transfer is forwarded, the place list is
//     published to the stores. The storage is PostgreSQL.
//
// The rules (pkg/stock) and the use cases are the same in both scopes; the difference is in Config
// and in the storage adapter.
package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/stock"
)

// Warehouse is the place id of the headquarters warehouse.
const Warehouse = "headquarters"

// WarehouseName is the name of the headquarters warehouse shown on the screen.
const WarehouseName = "Merkez deposu"

// ErrNotFound means the product was not found.
var ErrNotFound = errors.New("ürün bulunamadı")

// Row is a product's stock per place.
type Row struct {
	stock.Product
	// Qty is the stock by place id; a place with no movement is absent.
	Qty map[string]money.Qty
}

// Store is the stock records; the place (location) is a parameter. The store
// storage has a single place (only the store itself), the headquarters storage
// has many places.
type Store interface {
	// Record writes the movement to the place's stock and its events to the outbox in the same
	// transaction. If the movement id already exists at the same place it does nothing and returns
	// false. If the same delivery note of the same supplier is entered a second time
	// stock.ErrDuplicateReceipt is returned.
	Record(ctx context.Context, location string, m stock.Movement, events []outbox.Message) (bool, error)
	// Products returns the given products (one that cannot be found is skipped).
	Products(ctx context.Context, ids []string) (map[string]stock.Product, error)
	// Quantities is the stock of the given products at the place (zero for one with no movement).
	Quantities(ctx context.Context, location string, ids []string) (map[string]money.Qty, error)
	// Overview returns the products with their stock per place: those with q in the name or
	// whose barcode is q; all if q is empty. Sorted by name, at most limit.
	Overview(ctx context.Context, q string, limit int) ([]Row, error)
	// Places are the places that have stock.
	Places(ctx context.Context) ([]string, error)
	// ByBarcode is the barcode's product and its stock at the place; ErrNotFound if none.
	ByBarcode(ctx context.Context, location, barcode string) (stock.Item, error)
	// Movements returns the product's movements at the place (only with that product's line)
	// from newest to oldest, at most limit.
	Movements(ctx context.Context, location, productID string, limit int) ([]stock.Movement, error)
	// SaveProduct writes the product in the catalog (the last state is valid).
	SaveProduct(ctx context.Context, p stock.Product) error
	// EnsureProducts adds the products in a sale or movement line by name if they are
	// not in the catalog (it is corrected when the catalog arrives).
	EnsureProducts(ctx context.Context, lines []stock.Line) error

	// SendTransfer writes the outgoing movement to the place's stock, opens the transfer and
	// writes the events to the outbox; all in the same transaction. false if the movement already exists.
	SendTransfer(ctx context.Context, location string, m stock.Movement, t stock.Transfer, events []outbox.Message) (bool, error)
	// SaveTransfer writes the transfer sent here; does nothing if it already
	// exists (store: coming down from headquarters).
	SaveTransfer(ctx context.Context, t stock.Transfer) error
	// ReceiveTransfer writes the incoming movement to the place's stock and marks the transfer
	// received (in the same transaction); it writes the events to the outbox. false if the movement
	// already exists; stock.ErrTransferReceived if the transfer was received in the meantime.
	ReceiveTransfer(ctx context.Context, location string, m stock.Movement, events []outbox.Message) (bool, error)
	// Transfer, transferdir; yoksa stock.ErrUnknownTransfer.
	Transfer(ctx context.Context, id string) (stock.Transfer, error)
	// Transfers returns the ones on the way first, then the others from newest to oldest.
	Transfers(ctx context.Context, limit int) ([]stock.Transfer, error)
	// Enqueue writes the event to the outbox.
	Enqueue(ctx context.Context, e outbox.Message) error
}

// Locations is the storage of the places a transfer can be sent to in the store scope
// (headquarters publishes them).
type Locations interface {
	SetLocations(ctx context.Context, locs []stock.Location) error
	Locations(ctx context.Context) ([]stock.Location, error)
}

// Encoder turns the movement and the transfer into an event.
type Encoder interface {
	// Moved turns the movement into the event that will go to headquarters (store scope;
	// store.stock.moved.v1).
	Moved(m stock.Movement) (outbox.Message, error)
	// Transfer is the transfer sent to the receiving place (headquarters scope).
	Transfer(t stock.Transfer) (outbox.Message, error)
	// Locations are the places a transfer can be sent to (headquarters scope).
	Locations(locs []stock.Location) (outbox.Message, error)
}

// Limitler.
const (
	// StockLimit is the most products in the stock list.
	StockLimit = 500
	// HistoryLimit is the most movements in a product's history.
	HistoryLimit = 100
	// TransferLimit is the most rows in the transfer list.
	TransferLimit = 200
)

// Config is the service's scope.
type Config struct {
	// Here is the service's own place: the store id in the store scope,
	// Warehouse at headquarters.
	Here string
	// Headquarters is the headquarters scope.
	Headquarters bool
	// Stores are the stores connected to headquarters (headquarters scope; stores.json).
	Stores []stock.Location
	// Locations is the store of the place list in the store scope.
	Locations Locations
}

// Service, stok use case'leridir.
type Service struct {
	store Store
	enc   Encoder
	cfg   Config
	newID func() string
	now   func() time.Time

	// mu puts the movements in a queue: while a count reads the stock and writes the difference no
	// other movement gets in between (the difference would come out wrong); a transfer is not
	// received twice.
	mu sync.Mutex
}

// NewService binds the ports.
func NewService(store Store, enc Encoder, cfg Config, newID func() string, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, enc: enc, cfg: cfg, newID: newID, now: now}
}

// Here, servisin kendi yeridir.
func (s *Service) Here() string { return s.cfg.Here }

// Headquarters says that it is in the headquarters scope.
func (s *Service) Headquarters() bool { return s.cfg.Headquarters }

// local are the events of a movement at its own place: the movement event that goes to
// headquarters at a store; the movement of the headquarters warehouse is not reported anywhere else.
func (s *Service) local(m stock.Movement) ([]outbox.Message, error) {
	if s.cfg.Headquarters {
		return nil, nil
	}
	e, err := s.enc.Moved(m)
	if err != nil {
		return nil, err
	}
	return []outbox.Message{e}, nil
}

func (s *Service) record(ctx context.Context, m stock.Movement) (bool, error) {
	events, err := s.local(m)
	if err != nil {
		return false, err
	}
	return s.store.Record(ctx, s.cfg.Here, m, events)
}

func (s *Service) products(ctx context.Context, lines []stock.Line) (map[string]stock.Product, []string, error) {
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ProductID)
	}
	p, err := s.store.Products(ctx, ids)
	return p, ids, err
}

// ApplySale deducts a completed sale at the register from stock (store scope).
// If the same sale comes twice it is deducted once. Stock can go negative.
func (s *Service) ApplySale(ctx context.Context, saleID, cashierID string, at time.Time, lines []stock.Line) error {
	if len(lines) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.EnsureProducts(ctx, lines); err != nil {
		return err
	}
	_, err := s.record(ctx, stock.NewSale(saleID, cashierID, at, lines))
	return err
}

// Receive adds goods that arrive from a supplier with a delivery note to the stock of its own place.
func (s *Service) Receive(ctx context.Context, supplier, ref, by string, lines []stock.Line) (stock.Movement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	products, _, err := s.products(ctx, lines)
	if err != nil {
		return stock.Movement{}, err
	}
	m, err := stock.NewReceipt(s.newID(), supplier, ref, by, s.now(), lines, products)
	if err != nil {
		return stock.Movement{}, err
	}
	if _, err := s.record(ctx, m); err != nil {
		return stock.Movement{}, err
	}
	return m, nil
}

// Count processes a count: the difference between each product's counted quantity and the
// current stock is written to the stock.
func (s *Service) Count(ctx context.Context, ref, by string, counts []stock.Line) (stock.Movement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	products, ids, err := s.products(ctx, counts)
	if err != nil {
		return stock.Movement{}, err
	}
	system, err := s.store.Quantities(ctx, s.cfg.Here, ids)
	if err != nil {
		return stock.Movement{}, err
	}
	m, err := stock.NewCount(s.newID(), ref, by, s.now(), counts, system, products)
	if err != nil {
		return stock.Movement{}, err
	}
	if _, err := s.record(ctx, m); err != nil {
		return stock.Movement{}, err
	}
	return m, nil
}

// Overview is the stock list: the places in scope and the products' stock per
// place. visible filters the places that can be seen (its own place is always
// visible); q is a name or a barcode.
func (s *Service) Overview(ctx context.Context, q string, visible func(location string) bool) ([]string, []Row, error) {
	places, err := s.store.Places(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.store.Overview(ctx, q, StockLimit)
	if err != nil {
		return nil, nil, err
	}
	shown := []string{s.cfg.Here}
	for _, l := range places {
		if l != s.cfg.Here && visible(l) {
			shown = append(shown, l)
		}
	}
	for i := range rows {
		for l := range rows[i].Qty {
			if l != s.cfg.Here && !visible(l) {
				delete(rows[i].Qty, l)
			}
		}
	}
	return shown, rows, nil
}

// ByBarcode is the product of the scanned barcode (in goods receipt and counting).
func (s *Service) ByBarcode(ctx context.Context, barcode string) (stock.Item, error) {
	return s.store.ByBarcode(ctx, s.cfg.Here, barcode)
}

// History is the product's movements at a place.
func (s *Service) History(ctx context.Context, location, productID string) ([]stock.Movement, error) {
	return s.store.Movements(ctx, location, productID, HistoryLimit)
}

// ApplyProduct applies the product in headquarters' catalog (name, unit, barcode).
func (s *Service) ApplyProduct(ctx context.Context, p stock.Product) error {
	return s.store.SaveProduct(ctx, p)
}

// Locations are the places a transfer can be sent to (including itself). At headquarters
// the headquarters warehouse and the stores; at a store the list headquarters publishes.
func (s *Service) Locations(ctx context.Context) ([]stock.Location, error) {
	if s.cfg.Headquarters {
		return append([]stock.Location{{ID: Warehouse, Name: WarehouseName}}, s.cfg.Stores...), nil
	}
	if s.cfg.Locations == nil {
		return nil, nil
	}
	return s.cfg.Locations.Locations(ctx)
}

func (s *Service) known(ctx context.Context, id string) (bool, error) {
	locs, err := s.Locations(ctx)
	return slices.ContainsFunc(locs, func(l stock.Location) bool { return l.ID == id }), err
}

func (s *Service) isStore(id string) bool {
	return slices.ContainsFunc(s.cfg.Stores, func(l stock.Location) bool { return l.ID == id })
}

// Send sends a transfer from its own place to to: the outgoing movement is deducted from
// stock immediately; the transfer goes down to the receiving place (from a store through headquarters), and
// when the receiving place receives it, it is added to its stock.
func (s *Service) Send(ctx context.Context, to, by string, lines []stock.Line) (stock.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if to != "" && to != s.cfg.Here {
		ok, err := s.known(ctx, to)
		if err != nil {
			return stock.Transfer{}, err
		}
		if !ok {
			return stock.Transfer{}, stock.ErrUnknownLocation
		}
	}
	products, _, err := s.products(ctx, lines)
	if err != nil {
		return stock.Transfer{}, err
	}
	m, t, err := stock.NewTransferOut(s.newID(), s.newID(), s.cfg.Here, to, by, s.now(), lines, products)
	if err != nil {
		return stock.Transfer{}, err
	}
	var events []outbox.Message
	if s.cfg.Headquarters {
		// A transfer going from headquarters goes down to the store.
		e, err := s.enc.Transfer(t)
		if err != nil {
			return stock.Transfer{}, err
		}
		events = append(events, e)
	} else if events, err = s.local(m); err != nil {
		return stock.Transfer{}, err
	}
	if _, err := s.store.SendTransfer(ctx, s.cfg.Here, m, t, events); err != nil {
		return stock.Transfer{}, err
	}
	return t, nil
}

// ApplyTransferSent records the transfer coming down from headquarters (store scope):
// it is on the way until it is received. One sent to another place is skipped. If the product is
// not yet in this store's catalog it is added by name.
func (s *Service) ApplyTransferSent(ctx context.Context, t stock.Transfer) error {
	if t.To != s.cfg.Here || len(t.Lines) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.EnsureProducts(ctx, t.Lines); err != nil {
		return err
	}
	return s.store.SaveTransfer(ctx, t)
}

// ReceiveTransfer receives the transfer that came here: received are the received
// quantities (all of what was sent if empty). The incoming movement is added to stock.
func (s *Service) ReceiveTransfer(ctx context.Context, id, by string, received []stock.Line) (stock.Movement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.store.Transfer(ctx, id)
	if err != nil {
		return stock.Movement{}, err
	}
	products, _, err := s.products(ctx, t.Lines)
	if err != nil {
		return stock.Movement{}, err
	}
	m, err := stock.NewTransferIn(s.newID(), s.cfg.Here, by, s.now(), t, received, products)
	if err != nil {
		return stock.Movement{}, err
	}
	events, err := s.local(m)
	if err != nil {
		return stock.Movement{}, err
	}
	if _, err := s.store.ReceiveTransfer(ctx, s.cfg.Here, m, events); err != nil {
		return stock.Movement{}, err
	}
	return m, nil
}

// Transfers are the transfers; the ones leaving from or going to one of the given
// visible places are shown.
func (s *Service) Transfers(ctx context.Context, visible func(location string) bool) ([]stock.Transfer, error) {
	ts, err := s.store.Transfers(ctx, TransferLimit)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(ts, func(t stock.Transfer) bool { return !visible(t.From) && !visible(t.To) }), nil
}

// ApplyLocations applies the place list headquarters published (store).
func (s *Service) ApplyLocations(ctx context.Context, locs []stock.Location) error {
	if s.cfg.Locations == nil {
		return nil
	}
	return s.cfg.Locations.SetLocations(ctx, locs)
}

// PublishLocations publishes the place list to the stores (headquarters; at start,
// and the service is restarted when a store is added).
func (s *Service) PublishLocations(ctx context.Context) error {
	locs, _ := s.Locations(ctx)
	e, err := s.enc.Locations(locs)
	if err != nil {
		return err
	}
	return s.store.Enqueue(ctx, e)
}

// ApplyStoreMovement processes the movement coming from a store (store.stock.moved.v1) into that
// store's stock (headquarters scope). If the same movement comes twice it is counted
// once. A transfer out opens the transfer; a transfer sent to another store is
// forwarded to that store (one sent to the headquarters warehouse is received by the
// warehouse). A transfer in marks the transfer received.
func (s *Service) ApplyStoreMovement(ctx context.Context, location string, m stock.Movement) error {
	if !s.cfg.Headquarters {
		return errors.New("mağaza hareketi yalnızca merkezde işlenir")
	}
	if location == "" || location == Warehouse || m.ID == "" {
		return errors.New("mağaza hareketi geçersiz")
	}
	if err := s.store.EnsureProducts(ctx, m.Lines); err != nil {
		return err
	}
	switch {
	case m.Kind == stock.TransferOut && m.Ref != "" && m.Counterpart != "":
		t := stock.Transfer{ID: m.Ref, From: location, To: m.Counterpart, By: m.By, At: m.At}
		for _, l := range m.Lines {
			t.Lines = append(t.Lines, stock.Line{ProductID: l.ProductID, Name: l.Name, Qty: -l.Qty})
		}
		var events []outbox.Message
		if s.isStore(t.To) {
			e, err := s.enc.Transfer(t)
			if err != nil {
				return err
			}
			events = append(events, e)
		}
		_, err := s.store.SendTransfer(ctx, location, m, t, events)
		return err
	case m.Kind == stock.TransferIn:
		_, err := s.store.ReceiveTransfer(ctx, location, m, nil)
		return err
	}
	_, err := s.store.Record(ctx, location, m, nil)
	return err
}
