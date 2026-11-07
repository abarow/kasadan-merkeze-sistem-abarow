// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the use cases and ports of the restaurant module: the floor and
// tables, the check, sending to the kitchen, cancellation, the check printout, payment and
// bill splitting, moving and merging tables, the event stream of changes.
//
// All order work goes through this layer; the register screen and, in the future, the waiter's
// phone are clients of the same API (DESIGN section 20).
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/services/modules/restaurant/internal/domain"
)

var (
	// ErrProductNotFound means the product is not on the menu (it was withdrawn from sale or its
	// price is not in effect yet).
	ErrProductNotFound = errors.New("ürün menüde yok")
	// ErrNotAuthorized means the operation needs the manager's approval.
	ErrNotAuthorized = errors.New("müdür onayı gerekli")
)

// Person is the person doing the operation (a cashier or a waiter).
type Person struct{ ID, Name string }

// Store keeps the floor, tables and checks.
type Store interface {
	Floor(ctx context.Context) ([]domain.Area, error)
	SaveFloor(ctx context.Context, areas []domain.Area) error
	// OpenOrders are the open checks.
	OpenOrders(ctx context.Context) ([]*domain.Order, error)
	// Get returns the check; domain.ErrNotFound if none.
	Get(ctx context.Context, id string) (*domain.Order, error)
	// AtTable is the table's open check; domain.ErrNotFound if none.
	AtTable(ctx context.Context, tableID string) (*domain.Order, error)
	// Save writes the checks in one transaction. A table cannot have two open
	// checks (domain.ErrTableBusy).
	Save(ctx context.Context, orders ...*domain.Order) error
}

// MenuItem is a product on the menu.
type MenuItem struct {
	domain.Product
	Category    string
	ImageSHA256 string
}

// Image is the product's picture.
type Image struct {
	SHA256, ContentType string
	Data                []byte
}

// Menu, katalogdur (kasadaki catalog servisi).
type Menu interface {
	// Menu returns the products on the menu in menu order.
	Menu(ctx context.Context) ([]MenuItem, error)
	// Product returns the product with its current price; ErrProductNotFound if none.
	Product(ctx context.Context, id string) (domain.Product, error)
	// Image is the product's picture; domain.ErrNotFound if none.
	Image(ctx context.Context, id string) (Image, error)
}

// SaleLine is a line that goes to the sale.
type SaleLine struct {
	ProductID, Name string
	Qty             int
	UnitPrice       money.Money
	TaxRate         money.Rate
}

// SaleStatus is the state of the sale.
type SaleStatus string

const (
	SaleOpen    SaleStatus = "open"
	SaleCharged SaleStatus = "charged"
	SalePaid    SaleStatus = "paid"
	SaleVoided  SaleStatus = "voided"
)

// Sales is the sales service in the core.
type Sales interface {
	// CreateSale creates an open sale from the lines in the cashier's name.
	CreateSale(ctx context.Context, cashierID string, lines []SaleLine) (string, error)
	Status(ctx context.Context, saleID string) (SaleStatus, error)
}

// Printer is the printers at device: the receipt printer if the station is empty, otherwise
// the station's printer (the receipt printer if it has none).
type Printer interface {
	Columns(ctx context.Context) (int, error)
	Print(ctx context.Context, station, title string, lines []string) error
}

// Approver is the manager's approval (staff): if the person in the session is a manager or the approval
// key the manager gave is valid it returns the identity of the approving manager;
// otherwise ErrNotAuthorized.
type Approver interface {
	VoidLine(ctx context.Context, cashierID, token string) (string, error)
	EditFloor(ctx context.Context, cashierID, token string) (string, error)
}

// Event is a change: clients refresh their screens.
type Event struct {
	Type    string `json:"type"` // "order" or "floor"
	OrderID string `json:"order_id,omitempty"`
	TableID string `json:"table_id,omitempty"`
	Version int64  `json:"version,omitempty"`
}

// Service, restoran use case'leridir.
type Service struct {
	store    Store
	menu     Menu
	sales    Sales
	printer  Printer
	approver Approver
	newID    func() string
	// Now is the current time; it is replaced in tests.
	Now func() time.Time

	// mu puts the changes in a queue: if two terminals (phones in the future) change
	// the same check at the same time one does not overwrite the other.
	mu sync.Mutex

	subsMu sync.Mutex
	subs   map[chan Event]struct{}
}

// NewService binds the ports.
func NewService(store Store, menu Menu, sales Sales, printer Printer, approver Approver, newID func() string) *Service {
	return &Service{store: store, menu: menu, sales: sales, printer: printer, approver: approver, newID: newID,
		Now: time.Now, subs: map[chan Event]struct{}{}}
}

// Subscribe gives the change events until ctx ends. The event of a client
// that reads slowly is dropped (when the client reconnects it reads from the start).
func (s *Service) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 32)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	go func() {
		<-ctx.Done()
		s.subsMu.Lock()
		delete(s.subs, ch)
		close(ch)
		s.subsMu.Unlock()
	}()
	return ch
}

func (s *Service) publish(e Event) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *Service) published(orders ...*domain.Order) {
	for _, o := range orders {
		s.publish(Event{Type: "order", OrderID: o.ID, TableID: o.TableID, Version: o.Version})
	}
}

// Menu is the menu.
func (s *Service) Menu(ctx context.Context) ([]MenuItem, error) { return s.menu.Menu(ctx) }

// Image is the product's picture.
func (s *Service) Image(ctx context.Context, productID string) (Image, error) {
	return s.menu.Image(ctx, productID)
}

// TableView is the table and its open check (Order is nil if none).
type TableView struct {
	domain.Table
	Order *domain.Order
}

// AreaView is the floor area and its tables.
type AreaView struct {
	ID, Name string
	Tables   []TableView
}

// Floor returns the floor areas with their tables and their open checks. The sale of checks
// whose payment is in progress is looked at: if it was paid or cancelled it is applied to the check
// (if the register was closed before the payment screen closed).
func (s *Service) Floor(ctx context.Context) ([]AreaView, error) {
	areas, err := s.store.Floor(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := s.store.OpenOrders(ctx)
	if err != nil {
		return nil, err
	}
	byTable := map[string]*domain.Order{}
	for _, o := range orders {
		if o.Payment != nil {
			if o, err = s.Settle(ctx, o.ID); err != nil {
				return nil, err
			}
			if o.Closed {
				continue
			}
		}
		byTable[o.TableID] = o
	}
	out := make([]AreaView, 0, len(areas))
	for _, a := range areas {
		v := AreaView{ID: a.ID, Name: a.Name}
		for _, t := range a.Tables {
			v.Tables = append(v.Tables, TableView{Table: t, Order: byTable[t.ID]})
		}
		out = append(out, v)
	}
	return out, nil
}

// SetFloor changes the floor and tables (with the manager's approval). A table that
// has an open check cannot be removed.
func (s *Service) SetFloor(ctx context.Context, by Person, token string, areas []domain.Area) error {
	if err := domain.ValidateFloor(areas); err != nil {
		return err
	}
	if _, err := s.approver.EditFloor(ctx, by.ID, token); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	orders, err := s.store.OpenOrders(ctx)
	if err != nil {
		return err
	}
	for _, o := range orders {
		if !hasTable(areas, o.TableID) {
			return fmt.Errorf("%w: kaldırılan masada açık adisyon var", domain.ErrTableBusy)
		}
	}
	if err := s.store.SaveFloor(ctx, areas); err != nil {
		return err
	}
	s.publish(Event{Type: "floor"})
	return nil
}

func hasTable(areas []domain.Area, id string) bool {
	for _, a := range areas {
		if slices.ContainsFunc(a.Tables, func(t domain.Table) bool { return t.ID == id }) {
			return true
		}
	}
	return false
}

func (s *Service) tableName(ctx context.Context, id string) (string, error) {
	areas, err := s.store.Floor(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range areas {
		for _, t := range a.Tables {
			if t.ID == id {
				return t.Name, nil
			}
		}
	}
	return "", fmt.Errorf("%w: masa %s", domain.ErrNotFound, id)
}

// OpenTable opens a check at the table; if the table has an open check it returns it
// (if two terminals open at the same time a second check is not opened).
func (s *Service) OpenTable(ctx context.Context, tableID string, by Person) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.tableName(ctx, tableID); err != nil {
		return nil, err
	}
	if o, err := s.store.AtTable(ctx, tableID); err == nil {
		return o, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	o := domain.NewOrder(s.newID(), tableID, by.Name, s.Now())
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	return o, nil
}

// Order is the check (the sale of a check whose payment is in progress is looked at).
func (s *Service) Order(ctx context.Context, id string) (*domain.Order, error) {
	o, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.Payment != nil {
		return s.Settle(ctx, id)
	}
	return o, nil
}

// change reads the check, changes it with fn and saves it; the changes are
// applied in order.
func (s *Service) change(ctx context.Context, id string, fn func(*domain.Order) error) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	before := o.Version
	if err := fn(o); err != nil {
		return nil, err
	}
	if o.Version == before {
		return o, nil
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	return o, nil
}

// AddInput is the line to add. ID is the id the client produced: if the same
// request comes again a second line is not opened.
type AddInput struct {
	ID        string
	ProductID string
	Choices   []menu.Choice
	Note      string
	Qty       int
}

// AddLine adds a line to the check; the price and VAT are taken from the catalog now.
func (s *Service) AddLine(ctx context.Context, orderID string, in AddInput, by Person) (*domain.Order, error) {
	p, err := s.menu.Product(ctx, in.ProductID)
	if err != nil {
		return nil, err
	}
	return s.change(ctx, orderID, func(o *domain.Order) error {
		_, err := o.AddLine(domain.NewLineInput{ID: in.ID, Product: p, Choices: in.Choices, Note: in.Note, Qty: in.Qty, By: by.Name, At: s.Now()})
		return err
	})
}

// SetLine changes the quantity and note of an unsent line (quantity 0:
// the line is deleted).
func (s *Service) SetLine(ctx context.Context, orderID, lineID string, qty int, note string) (*domain.Order, error) {
	return s.change(ctx, orderID, func(o *domain.Order) error { return o.SetLine(lineID, qty, note) })
}

// Send sends the waiting lines to the kitchen: every station's ticket is printed on its own
// printer, and the printed lines become sent. If a station's
// printer cannot print, its lines wait (when sent again only
// those are printed) and an error is returned.
func (s *Service) Send(ctx context.Context, orderID string, by Person) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	table, err := s.tableName(ctx, o.TableID)
	if err != nil {
		return nil, err
	}
	cols, err := s.printer.Columns(ctx)
	if err != nil {
		return nil, err
	}
	var failed error
	sent := false
	for _, station := range o.Stations() {
		lines, err := o.Send(s.Now(), station)
		if err != nil {
			return nil, err
		}
		title, doc := domain.KitchenTicket(cols, station, table, by.Name, s.Now(), lines, false)
		if err := s.printer.Print(ctx, station, title, doc); err != nil {
			failed = errors.Join(failed, fmt.Errorf("%s fişi basılamadı, satırları bekliyor: %w", domain.StationName(station), err))
			// This station's lines stay unsent: the last saved state.
			if o, err = s.store.Get(ctx, orderID); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.store.Save(ctx, o); err != nil {
			return nil, err
		}
		sent = true
	}
	if sent {
		s.published(o)
	}
	return o, failed
}

// Void cancels qty of a sent line with the manager's approval;
// a cancellation ticket is printed at the station.
func (s *Service) Void(ctx context.Context, orderID, lineID string, qty int, reason string, by Person, token string) (*domain.Order, error) {
	manager, err := s.approver.VoidLine(ctx, by.ID, token)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	table, err := s.tableName(ctx, o.TableID)
	if err != nil {
		return nil, err
	}
	who := by.Name
	if manager != by.ID {
		who = fmt.Sprintf("%s (onay: %s)", by.Name, manager)
	}
	v, err := o.Void(lineID, s.newID(), qty, reason, who)
	if err != nil {
		return nil, err
	}
	cols, err := s.printer.Columns(ctx)
	if err != nil {
		return nil, err
	}
	title, doc := domain.KitchenTicket(cols, v.Station, table, by.Name, s.Now(), []domain.Line{v}, true)
	if err := s.printer.Print(ctx, v.Station, title, doc); err != nil {
		return nil, fmt.Errorf("iptal fişi basılamadı; iptal yapılmadı: %w", err)
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	return o, nil
}

// Bill prints the check printout (the bill) on the receipt printer. It has no fiscal value.
func (s *Service) Bill(ctx context.Context, orderID string, by Person) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	table, err := s.tableName(ctx, o.TableID)
	if err != nil {
		return nil, err
	}
	cols, err := s.printer.Columns(ctx)
	if err != nil {
		return nil, err
	}
	if err := o.RequestBill(s.Now()); err != nil {
		return nil, err
	}
	title, doc := domain.BillDocument(cols, table, by.Name, s.Now(), o)
	if err := s.printer.Print(ctx, "", title, doc); err != nil {
		return nil, fmt.Errorf("adisyon basılamadı: %w", err)
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	return o, nil
}

// Pay turns the whole check or the selected lines (bill splitting) into a sale:
// those quantities are in payment until the sale is paid or cancelled.
// version is the version the client saw (0: not checked). The id of the sale
// is returned; the payment screen is opened with it.
func (s *Service) Pay(ctx context.Context, orderID string, version int64, parts []domain.Part, by Person) (*domain.Order, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, "", err
	}
	if err := o.Check(version); err != nil {
		return nil, "", err
	}
	parts, err = o.Parts(parts)
	if err != nil {
		return nil, "", err
	}
	var lines []SaleLine
	for _, p := range parts {
		for _, l := range o.Lines {
			if l.ID == p.LineID {
				lines = append(lines, SaleLine{ProductID: l.ProductID, Name: l.Label(), Qty: p.Qty, UnitPrice: l.UnitPrice, TaxRate: l.TaxRate})
			}
		}
	}
	saleID, err := s.sales.CreateSale(ctx, by.ID, lines)
	if err != nil {
		return nil, "", err
	}
	if err := o.Hold(saleID, parts, by.Name, s.Now()); err != nil {
		return nil, "", err
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, "", err
	}
	s.published(o)
	return o, saleID, nil
}

// Settle looks at the sale of the payment in progress: if paid the quantities are paid (if all are
// paid the check closes, the table is freed), if cancelled it returns to
// the check; if the sale is still in progress it does nothing.
func (s *Service) Settle(ctx context.Context, orderID string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil || o.Payment == nil {
		return o, err
	}
	st, err := s.sales.Status(ctx, o.Payment.SaleID)
	if err != nil {
		return nil, err
	}
	switch st {
	case SalePaid:
		o.Settle(true, s.Now())
	case SaleVoided:
		o.Settle(false, s.Now())
	default:
		return o, nil
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	return o, nil
}

// Move moves the check to an empty table.
func (s *Service) Move(ctx context.Context, orderID string, version int64, tableID string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.tableName(ctx, tableID); err != nil {
		return nil, err
	}
	if _, err := s.store.AtTable(ctx, tableID); err == nil {
		return nil, domain.ErrTableBusy
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if err := o.Check(version); err != nil {
		return nil, err
	}
	from := o.TableID
	if err := o.MoveTo(tableID); err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, err
	}
	s.published(o)
	s.publish(Event{Type: "order", OrderID: o.ID, TableID: from, Version: o.Version})
	return o, nil
}

// Merge merges another table's check into this one; the other table is freed.
func (s *Service) Merge(ctx context.Context, orderID string, version int64, fromID string, fromVersion int64) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	other, err := s.store.Get(ctx, fromID)
	if err != nil {
		return nil, err
	}
	if err := o.Check(version); err != nil {
		return nil, err
	}
	if err := other.Check(fromVersion); err != nil {
		return nil, err
	}
	if o.ID == other.ID {
		return nil, fmt.Errorf("%w: adisyon kendisiyle birleştirilemez", domain.ErrInvalidLine)
	}
	if err := o.Absorb(other, s.Now()); err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, o, other); err != nil {
		return nil, err
	}
	s.published(o, other)
	return o, nil
}

// CloseEmpty closes a check with no lines (a table opened by mistake).
func (s *Service) CloseEmpty(ctx context.Context, orderID string) (*domain.Order, error) {
	return s.change(ctx, orderID, func(o *domain.Order) error { return o.CloseEmpty(s.Now()) })
}
