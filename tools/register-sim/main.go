// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command register-sim has the sales service perform the operations a cashier would do over
// gRPC and checks the results. It is used in the Docker simulation:
//
//	docker compose -f deploy/compose/compose.yaml run --rm register-sim
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"
	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/sequence"
	"abarow.com/pkg/service"
)

type scenario struct {
	name string
	run  func(ctx context.Context, c salesv1.SalesServiceClient) (string, error)
}

func main() {
	addr := service.Env("ABAROW_SALES_ADDR", "localhost:9000")
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()
	c := salesv1.NewSalesServiceClient(conn)

	// The store server scenario may wait for the source connection to be set up.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	gw := os.Getenv("ABAROW_GATEWAY_URL")
	natsURL := service.Env("ABAROW_NATS_URL", "nats://localhost:4222")
	events := os.Getenv("ABAROW_SIM_SKIP_EVENTS") == "" // the event check is skipped while NATS is down

	// The requests at the gateway ask for a cashier session; the end of day asks for a manager.
	if gw != "" {
		var err error
		if cashierToken, err = login(ctx, gw, "ayse", "1111"); err == nil {
			managerToken, err = login(ctx, gw, "ali", "9999")
		}
		if err == nil {
			err = closeLeftoverShift(ctx, gw)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not sign in:", err)
			os.Exit(1)
		}
	}
	// The end-of-day scenario compares this run's sales in a separate period:
	// first a Z is taken and a clean day is opened.
	if gw != "" && events {
		if err := startDay(ctx, gw, natsURL); err != nil {
			fmt.Fprintln(os.Stderr, "could not start the day:", err)
			os.Exit(1)
		}
	}
	// The receipt printer and the drawer are in the device service.
	if deviceAddr = os.Getenv("ABAROW_DEVICE_ADDR"); deviceAddr != "" {
		scenarios = append(scenarios, deviceScenario(deviceAddr))
	}
	// The requests the screen makes are tried through the gateway.
	if gw != "" {
		scenarios = append(scenarios, authScenario(gw), shiftOpenScenario(gw), marketScenario(gw), cashMovementScenario(gw))
	}
	// Shift closing and the end of day wait for the events to be processed; they are not tried
	// while the message path is cut (while the event check is skipped). The closing is requested right
	// after the market sale.
	if events {
		if gw != "" {
			scenarios = append(scenarios, shiftCloseScenario(gw))
		}
		scenarios = append(scenarios, eventsScenario(natsURL))
		if gw != "" {
			scenarios = append(scenarios, staffEventsScenario(natsURL))
		}
		if os.Getenv("ABAROW_NATS_NKEY") != "" {
			scenarios = append(scenarios, natsAuthScenario(natsURL))
		}
		if gw != "" {
			scenarios = append(scenarios, reportScenario(gw, natsURL))
		}
		// At a register connected to the store server (the store simulation) it is checked that the sales
		// reach the store and do not get mixed up between registers.
		if store := os.Getenv("ABAROW_STORE_NATS_URL"); store != "" {
			scenarios = append(scenarios, storeScenario(natsURL, store, os.Getenv("ABAROW_STORE_REPORT_URL")))
		}
		// Store stock: a sale deducts from stock; goods receipt and count go through the store server
		// screen's API (report → inventory).
		if r := os.Getenv("ABAROW_STORE_REPORT_URL"); r != "" && os.Getenv("ABAROW_STORE_STOCK") != "" {
			scenarios = append(scenarios, stockScenario(r))
		}
		// In a store whose store server is connected to headquarters the events reach headquarters too.
		if ingest := os.Getenv("ABAROW_HEADQUARTERS_INGEST_URL"); ingest != "" {
			scenarios = append(scenarios, headquartersScenario(natsURL, ingest))
		}
		// The store server's day report and headquarters' summary come from the same sales ledger;
		// for the same day and store the figures must be exactly the same.
		if reporting := os.Getenv("ABAROW_HEADQUARTERS_REPORTING_URL"); reporting != "" {
			scenarios = append(scenarios, ledgerScenario(os.Getenv("ABAROW_STORE_REPORT_URL"), reporting))
		}
		// A product added at headquarters comes down to the register's catalog.
		if spec := os.Getenv("ABAROW_HEADQUARTERS_PRODUCT"); spec != "" {
			scenarios = append(scenarios, headquartersCatalogScenario(service.Env("ABAROW_CATALOG_ADDR", "catalog:9000"), spec))
		}
		// A store price given at the store server comes down to the register.
		if spec := os.Getenv("ABAROW_STORE_PRICE"); spec != "" {
			scenarios = append(scenarios, storePriceScenario(service.Env("ABAROW_CATALOG_ADDR", "catalog:9000"),
				os.Getenv("ABAROW_STORE_REPORT_URL"), spec))
		}
		// Product management at the store server: while headquarters counts as connected it is closed
		// to writing; when the connection is closed the product added comes down to the register's catalog.
		// In a setup with headquarters both registers use the same store server; the product code
		// is specific to the register.
		if os.Getenv("ABAROW_STORE_CATALOG") != "" {
			scenarios = append(scenarios, storeCatalogScenario(service.Env("ABAROW_CATALOG_ADDR", "catalog:9000"),
				os.Getenv("ABAROW_STORE_REPORT_URL")))
		}
		// A cashier added at the store server signs in at the register; when the account is closed
		// they cannot, the open session closes.
		if os.Getenv("ABAROW_STORE_CASHIERS") != "" && gw != "" {
			scenarios = append(scenarios, storeCashierScenario(gw, os.Getenv("ABAROW_STORE_REPORT_URL"),
				"store-"+service.Env("ABAROW_REGISTER_ID", "register")))
		}
	}

	fmt.Printf("abarow register simulation — sales: %s\n\n", addr)
	failed := 0
	for _, sc := range scenarios {
		detail, err := sc.run(ctx, c)
		if err != nil {
			failed++
			fmt.Printf("✗ %s\n    %v\n", sc.name, err)
			continue
		}
		fmt.Printf("✓ %s\n    %s\n", sc.name, detail)
	}
	fmt.Printf("\n%d scenarios, %d failed\n", len(scenarios), failed)
	if failed > 0 {
		os.Exit(1)
	}
}

var (
	detergent = &salesv1.SaleLine{ProductId: "p1", Barcode: "8690000000011", Name: "Deterjan 1 L", QtyMilli: 1000, UnitPriceKurus: 1299, TaxRateBp: 2000}
	tomato    = &salesv1.SaleLine{ProductId: "p2", Barcode: "2700001012500", Name: "Domates", QtyMilli: 1250, UnitPriceKurus: 8990, TaxRateBp: 100}
	bread     = &salesv1.SaleLine{ProductId: "p3", Name: "EKMEK", QtyMilli: 1000, UnitPriceKurus: 1200, TaxRateBp: 100}
	shoes     = &salesv1.SaleLine{ProductId: "p4", Name: "AYAKKABI", QtyMilli: 1000, UnitPriceKurus: 2200, TaxRateBp: 1000}
	shampoo   = &salesv1.SaleLine{ProductId: "p5", Name: "ŞAMPUAN", QtyMilli: 1000, UnitPriceKurus: 1500, TaxRateBp: 2000}
	tv        = &salesv1.SaleLine{ProductId: "p6", Name: "Televizyon", QtyMilli: 1000, UnitPriceKurus: 600000, TaxRateBp: 2000}
)

func cash(kurus int64) *salesv1.Payment {
	return &salesv1.Payment{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: kurus}
}

func card(kurus int64) *salesv1.Payment {
	return &salesv1.Payment{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CARD, AmountKurus: kurus}
}

func mealCard(kurus int64) *salesv1.Payment {
	return &salesv1.Payment{Method: salesv1.PaymentMethod_PAYMENT_METHOD_MEAL_CARD, AmountKurus: kurus}
}

func tl(kurus int64) string { return money.Money(kurus).String() + " TL" }

var scenarios = []scenario{
	{"Cash sale, change", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		s, err := sell(ctx, c, []*salesv1.SaleLine{detergent, tomato}, cash(20000))
		if err != nil {
			return "", err
		}
		if s.GetTotalKurus() != 12537 || s.GetChangeKurus() != 7463 {
			return "", fmt.Errorf("total %s, change %s; want 125.37 / 74.63", tl(s.GetTotalKurus()), tl(s.GetChangeKurus()))
		}
		return describe(s), nil
	}},
	{"GİB example receipt: cash + card", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		s, err := sell(ctx, c, []*salesv1.SaleLine{bread, shoes, shampoo}, cash(2900), card(2000))
		if err != nil {
			return "", err
		}
		if s.GetTaxTotalKurus() != 462 || s.GetTotalKurus() != 4900 {
			return "", fmt.Errorf("TOPKDV %s, TOPLAM %s; istenen 4,62 / 49,00", tl(s.GetTaxTotalKurus()), tl(s.GetTotalKurus()))
		}
		if ref := s.GetPayments()[1].GetRef(); !strings.HasPrefix(ref, "SIMPOS-") {
			return "", fmt.Errorf("the card payment has no POS reference: %q", ref)
		}
		return describe(s) + ", kart ref " + s.GetPayments()[1].GetRef(), nil
	}},
	{"Meal card + cash", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		// 49.00: 40.00 from the meal card, the remaining 9.00 cash; no change.
		s, err := sell(ctx, c, []*salesv1.SaleLine{bread, shoes, shampoo}, mealCard(4000), cash(900))
		if err != nil {
			return "", err
		}
		meal := s.GetPayments()[0]
		if meal.GetMethod() != salesv1.PaymentMethod_PAYMENT_METHOD_MEAL_CARD || !strings.HasPrefix(meal.GetRef(), "SIMPOS-") || s.GetChangeKurus() != 0 {
			return "", fmt.Errorf("meal card payment: %v ref %q, change %s", meal.GetMethod(), meal.GetRef(), tl(s.GetChangeKurus()))
		}
		mealTotal += meal.GetAmountKurus()
		// The excess cannot be taken from the meal card (change only from cash).
		created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: []*salesv1.SaleLine{bread}})
		if err != nil {
			return "", err
		}
		_, err = c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId(), Payments: []*salesv1.Payment{mealCard(5000)}})
		if status.Code(err) != codes.InvalidArgument {
			return "", fmt.Errorf("an excess meal card payment was accepted: %v", err)
		}
		if _, err := c.VoidSale(ctx, &salesv1.VoidSaleRequest{SaleId: created.GetSale().GetId()}); err != nil {
			return "", err
		}
		recordVoided(created.GetSale().GetId(), created.GetSale().GetTotalKurus())
		return describe(s) + ", meal card ref" + meal.GetRef() + "; the excess meal card payment was rejected", nil
	}},
	{"An unpaid sale is cancelled", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: []*salesv1.SaleLine{detergent}})
		if err != nil {
			return "", err
		}
		voided, err := c.VoidSale(ctx, &salesv1.VoidSaleRequest{SaleId: created.GetSale().GetId()})
		if err != nil {
			return "", err
		}
		if voided.GetSale().GetStatus() != salesv1.SaleStatus_SALE_STATUS_VOIDED {
			return "", fmt.Errorf("durum %v", voided.GetSale().GetStatus())
		}
		recordVoided(voided.GetSale().GetId(), voided.GetSale().GetTotalKurus())
		return "sale" + short(voided.GetSale().GetId()) + " iptal edildi", nil
	}},
	{"An insufficient payment is rejected", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: []*salesv1.SaleLine{detergent}})
		if err != nil {
			return "", err
		}
		_, err = c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId(), Payments: []*salesv1.Payment{cash(100)}})
		if status.Code(err) != codes.InvalidArgument {
			return "", fmt.Errorf("kod %v, istenen InvalidArgument", status.Code(err))
		}
		msg := status.Convert(err).Message()
		if _, err := c.VoidSale(ctx, &salesv1.VoidSaleRequest{SaleId: created.GetSale().GetId()}); err != nil {
			return "", err
		}
		recordVoided(created.GetSale().GetId(), created.GetSale().GetTotalKurus())
		return "the 1.00 TL payment was rejected (" + msg + ")", nil
	}},
	{"A card over the limit is declined, completed with cash", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: []*salesv1.SaleLine{tv}})
		if err != nil {
			return "", err
		}
		id := created.GetSale().GetId()
		_, err = c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: id, Payments: []*salesv1.Payment{card(600000)}})
		if status.Code(err) != codes.FailedPrecondition {
			return "", fmt.Errorf("kod %v, istenen FailedPrecondition (POS reddi)", status.Code(err))
		}
		got, err := c.GetSale(ctx, &salesv1.GetSaleRequest{SaleId: id})
		if err != nil {
			return "", err
		}
		if got.GetSale().GetStatus() != salesv1.SaleStatus_SALE_STATUS_OPEN {
			return "", fmt.Errorf("reddedilen karttan sonra durum %v, istenen OPEN", got.GetSale().GetStatus())
		}
		done, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: id, Payments: []*salesv1.Payment{cash(600000)}})
		if err != nil {
			return "", err
		}
		recordCompleted(id, done.GetSale().GetTotalKurus())
		return "card declined, closed with cash:" + describe(done.GetSale()), nil
	}},
}

// sell creates a sale and closes it with the given payments.
func sell(ctx context.Context, c salesv1.SalesServiceClient, lines []*salesv1.SaleLine, payments ...*salesv1.Payment) (*salesv1.Sale, error) {
	created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: lines})
	if err != nil {
		return nil, fmt.Errorf("could not create the sale: %w", err)
	}
	done, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId(), Payments: payments})
	if err != nil {
		return nil, fmt.Errorf("could not take the payment: %w", err)
	}
	s := done.GetSale()
	if s.GetStatus() != salesv1.SaleStatus_SALE_STATUS_PAID || s.GetReceiptNo() == 0 || !strings.HasPrefix(s.GetFiscalNo(), "SIM-") {
		return nil, fmt.Errorf("the sale did not close: status %v, receipt no %d, fiscal no %q", s.GetStatus(), s.GetReceiptNo(), s.GetFiscalNo())
	}
	recordCompleted(s.GetId(), s.GetTotalKurus())
	return s, nil
}

// deviceAddr is the device service's address; if empty the print counter is not checked.
var deviceAddr string

// printed returns the number of documents device printed; -1 if there is no device.
func printed(ctx context.Context) (int64, error) {
	if deviceAddr == "" {
		return -1, nil
	}
	conn, err := grpc.NewClient(deviceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	st, err := devicev1.NewDeviceServiceClient(conn).GetStatus(ctx, &devicev1.GetStatusRequest{})
	return st.GetPrinted(), err
}

// printVia has the gateway print a report and checks that device printed a
// document.
func printVia(ctx context.Context, token, url string) error {
	before, err := printed(ctx)
	if err != nil {
		return err
	}
	if err := call(ctx, http.MethodPost, token, url, nil, nil); err != nil {
		return err
	}
	after, err := printed(ctx)
	if err != nil {
		return err
	}
	if after != before+1 {
		return fmt.Errorf("%s: documents printed by device %d→%d, want +1", url, before, after)
	}
	return nil
}

// deviceScenario checks that the receipt was printed on device's printer, that the drawer opened
// only on a receipt with cash, that a wide line from the printer was rejected and that
// a printer that ran out of paper does not print.
func deviceScenario(addr string) scenario {
	return scenario{"Peripherals: the receipt goes to the printer, the drawer opens only with cash", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return "", err
		}
		defer conn.Close()
		d := devicev1.NewDeviceServiceClient(conn)
		readStatus := func() (*devicev1.GetStatusResponse, error) {
			return d.GetStatus(ctx, &devicev1.GetStatusRequest{})
		}

		before, err := readStatus()
		if err != nil {
			return "", err
		}
		if _, err := sell(ctx, c, []*salesv1.SaleLine{bread}, card(1200)); err != nil {
			return "", err
		}
		mid, err := readStatus()
		if err != nil {
			return "", err
		}
		if mid.GetPrinted() != before.GetPrinted()+1 || mid.GetDrawerOpened() != before.GetDrawerOpened() {
			return "", fmt.Errorf("receipt with card: printed %d→%d, drawer %d→%d; want +1 print, drawer closed",
				before.GetPrinted(), mid.GetPrinted(), before.GetDrawerOpened(), mid.GetDrawerOpened())
		}
		if _, err := sell(ctx, c, []*salesv1.SaleLine{bread}, cash(2000)); err != nil {
			return "", err
		}
		after, err := readStatus()
		if err != nil {
			return "", err
		}
		if after.GetPrinted() != mid.GetPrinted()+1 || after.GetDrawerOpened() != mid.GetDrawerOpened()+1 {
			return "", fmt.Errorf("receipt with cash: printed %d→%d, drawer %d→%d; want +1 print, +1 drawer",
				mid.GetPrinted(), after.GetPrinted(), mid.GetDrawerOpened(), after.GetDrawerOpened())
		}
		wide := strings.Repeat("x", int(after.GetColumns())+1)
		if _, err := d.Print(ctx, &devicev1.PrintRequest{Title: "test", Lines: []string{wide}}); status.Code(err) != codes.InvalidArgument {
			return "", fmt.Errorf("wide line from the printer: %v", err)
		}
		// The paper runs out: the state is reported, the document is not printed; it prints when the paper is loaded.
		doc := &devicev1.PrintRequest{Title: "test", Lines: []string{"paper trial"}}
		if _, err := d.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState_PRINTER_STATE_PAPER_OUT}); err != nil {
			return "", err
		}
		out, err := readStatus()
		if err != nil {
			return "", err
		}
		_, perr := d.Print(ctx, doc)
		if _, err := d.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState_PRINTER_STATE_READY}); err != nil {
			return "", err
		}
		if out.GetPrinterState() != devicev1.PrinterState_PRINTER_STATE_PAPER_OUT || out.GetPrinterMessage() != "Kağıt bitti" {
			return "", fmt.Errorf("paper out: state %v %q", out.GetPrinterState(), out.GetPrinterMessage())
		}
		if status.Code(perr) != codes.Unavailable || !strings.Contains(perr.Error(), "Kağıt bitti") {
			return "", fmt.Errorf("printing without paper: %v", perr)
		}
		if _, err := d.Print(ctx, doc); err != nil {
			return "", fmt.Errorf("after the paper was loaded: %v", err)
		}
		return fmt.Sprintf("printer %s, %d columns; the receipt with card was printed, the drawer did not open; the receipt with cash was printed, the drawer opened; a line of %d characters was rejected; it did not print when the paper ran out (%q), it printed once the paper was loaded",
			after.GetPrinter(), after.GetColumns(), len(wide), out.GetPrinterMessage()), nil
	}}
}

// marketScenario makes the requests of the register screen through the gateway:
// it opens a basket, scans a barcode and a scale barcode, pays with cash.
func marketScenario(gw string) scenario {
	return scenario{"Market flow (gateway): barcode, scale barcode, cash", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		var basket struct {
			ID         string `json:"id"`
			SaleID     string `json:"sale_id"`
			TotalKurus int64  `json:"total_kurus"`
			Lines      []struct {
				Name string `json:"name"`
			} `json:"lines"`
		}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/module/baskets", nil, &basket); err != nil {
			return "", err
		}
		items := gw + "/api/module/baskets/" + basket.ID + "/items"
		if err := call(ctx, http.MethodPost, cashierToken, items, map[string]string{"barcode": "0000000000000"}, nil); err == nil || !strings.Contains(err.Error(), "404") {
			return "", fmt.Errorf("bilinmeyen barkod reddedilmedi: %v", err)
		}
		for _, code := range []string{"8690000000012", "8690000000012", "2700001012504"} {
			if err := call(ctx, http.MethodPost, cashierToken, items, map[string]string{"barcode": code}, &basket); err != nil {
				return "", fmt.Errorf("%s: %w", code, err)
			}
		}
		// 2 × Ekmek 15,00 + Domates 39,90/kg × 1,250 kg (49,875 → 49,88)
		if len(basket.Lines) != 2 || basket.TotalKurus != 7988 {
			return "", fmt.Errorf("basket is wrong: %d lines, total %s; want 2 lines, 79.88 TL", len(basket.Lines), tl(basket.TotalKurus))
		}
		// If the screen is reopened the cashier continues from this basket.
		var current struct {
			ID         string `json:"id"`
			TotalKurus int64  `json:"total_kurus"`
		}
		if err := call(ctx, http.MethodGet, cashierToken, gw+"/api/module/baskets/current", nil, &current); err != nil {
			return "", err
		}
		if current.ID != basket.ID || current.TotalKurus != basket.TotalKurus {
			return "", fmt.Errorf("devam edilecek sepet %s (%s), istenen %s", current.ID, tl(current.TotalKurus), basket.ID)
		}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/module/baskets/"+basket.ID+"/checkout", nil, &basket); err != nil {
			return "", err
		}

		var sale struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			CashierID   string `json:"cashier_id"`
			ReceiptNo   int64  `json:"receipt_no"`
			FiscalNo    string `json:"fiscal_no"`
			ChangeKurus int64  `json:"change_kurus"`
		}
		payment := map[string]any{"payments": []map[string]any{{"method": "cash", "amount_kurus": 10000}}}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/sales/"+basket.SaleID+"/checkout", payment, &sale); err != nil {
			return "", err
		}
		if sale.Status != "paid" || sale.ChangeKurus != 2012 || sale.CashierID != "ayse" {
			return "", fmt.Errorf("sale %s, change %s, cashier %q; want paid, 20.12 TL, ayse", sale.Status, tl(sale.ChangeKurus), sale.CashierID)
		}
		recordCompleted(sale.ID, basket.TotalKurus)
		shiftReceipts++
		shiftCash += basket.TotalKurus // cash payment, change deducted
		return fmt.Sprintf("an unknown barcode was rejected; the basket has 2 lines, total %s (the basket the cashier will continue with); receipt no %d, fiscal no %s, change %s, cashier %s",
			tl(basket.TotalKurus), sale.ReceiptNo, sale.FiscalNo, tl(sale.ChangeKurus), sale.CashierID), nil
	}}
}

// Gateway sessions: the market flow is done with the cashier, the end of day with the manager.
var cashierToken, managerToken string

// The shift the cashier opened in this run: the opening cash, the number of sales
// made through the gateway and the cash that entered the drawer.
const openingCash = 50000

var shiftReceipts, shiftCash int64

// The cash put into and taken out of the register in the shift; drawer openings
// without a sale.
var cashIn, cashOut, drawerOpens int64

// shift is the gateway's shift JSON.
type shift struct {
	ID               int64  `json:"id"`
	CashierID        string `json:"cashier_id"`
	CashierName      string `json:"cashier_name"`
	OpeningCashKurus int64  `json:"opening_cash_kurus"`
	CountedCashKurus int64  `json:"counted_cash_kurus"`
	ClosedBy         string `json:"closed_by"`
}

// closeLeftoverShift closes the shift left open from the previous run with the manager;
// the scenarios start with an empty register.
func closeLeftoverShift(ctx context.Context, gw string) error {
	var cur struct {
		Shift *shift `json:"shift"`
	}
	if err := call(ctx, http.MethodGet, managerToken, gw+"/api/shift", nil, &cur); err != nil || cur.Shift == nil {
		return err
	}
	return call(ctx, http.MethodPost, managerToken, gw+"/api/shift/close", map[string]int64{"counted_cash_kurus": 0}, nil)
}

// shiftOpenScenario checks that a sale without a shift is rejected, that the shift
// is opened and that a second shift cannot be opened at the register.
func shiftOpenScenario(gw string) scenario {
	return scenario{"A shift is opened; a sale without a shift and a second shift are rejected", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/module/baskets", nil, nil); err == nil || !strings.Contains(err.Error(), "409") {
			return "", fmt.Errorf("a basket could be opened without a shift: %v", err)
		}
		var sh shift
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/shift/open", map[string]int64{"opening_cash_kurus": openingCash}, &sh); err != nil {
			return "", err
		}
		if sh.CashierID != "ayse" || sh.OpeningCashKurus != openingCash {
			return "", fmt.Errorf("the shift was opened wrongly: %+v", sh)
		}
		mert, err := login(ctx, gw, "mert", "2222")
		if err != nil {
			return "", err
		}
		err = call(ctx, http.MethodPost, mert, gw+"/api/shift/open", map[string]int64{"opening_cash_kurus": 0}, nil)
		if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "Kasiyer Ayşe") {
			return "", fmt.Errorf("a second shift could be opened: %v", err)
		}
		if err := call(ctx, http.MethodPost, mert, gw+"/api/module/baskets", nil, nil); err == nil || !strings.Contains(err.Error(), "409") {
			return "", fmt.Errorf("a basket could be opened in someone else's shift: %v", err)
		}
		return fmt.Sprintf("shift no %d, opening cash %s; a basket without a shift 409; Mert's shift and basket 409", sh.ID, tl(sh.OpeningCashKurus)), nil
	}}
}

// cashMovementScenario checks the cash movements and their permissions:
// the cashier puts money into the register but cannot take it out; the manager takes interim cash.
func cashMovementScenario(gw string) scenario {
	return scenario{"Cash movements: the cashier puts money in, the manager takes interim cash", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		move := func(token, kind string, amount int64, note string) error {
			return call(ctx, http.MethodPost, token, gw+"/api/shift/cash", map[string]any{"kind": kind, "amount_kurus": amount, "note": note}, nil)
		}
		if err := move(cashierToken, "in", 10000, "Bozuk para"); err != nil {
			return "", err
		}
		cashIn += 10000
		if err := move(cashierToken, "out", 5000, "Kendine ödeme"); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("kasiyer kasadan para alabildi: %v", err)
		}
		if err := move(managerToken, "out", 0, "Ara para"); err == nil || !strings.Contains(err.Error(), "422") {
			return "", fmt.Errorf("a zero amount was accepted: %v", err)
		}
		if err := move(managerToken, "out", 20000, " "); err == nil || !strings.Contains(err.Error(), "422") {
			return "", fmt.Errorf("a movement with no description was accepted: %v", err)
		}
		if err := move(managerToken, "out", 20000, "Ara para (çelik kasaya)"); err != nil {
			return "", err
		}
		cashOut += 20000

		// Manager approval: a one-time authority with the manager's PIN in the cashier's session.
		approve := func(pin string) (string, error) {
			var out struct {
				Token string `json:"approval_token"`
			}
			err := call(ctx, http.MethodPost, cashierToken, gw+"/api/auth/approve",
				map[string]string{"manager_id": "ali", "pin": pin, "action": "cash_out"}, &out)
			return out.Token, err
		}
		// A wrong PIN is 403 (if it were 401 the screen would close the cashier's session).
		if _, err := approve("0000"); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("wrong approval PIN: %v", err)
		}
		approval, err := approve("9999")
		if err != nil {
			return "", err
		}
		body := map[string]any{"kind": "out", "amount_kurus": 5000, "note": "Masraf (müdür onayıyla)"}
		if err := callApproved(ctx, http.MethodPost, cashierToken, approval, gw+"/api/shift/cash", body, nil); err != nil {
			return "", err
		}
		cashOut += 5000
		if err := callApproved(ctx, http.MethodPost, cashierToken, approval, gw+"/api/shift/cash", body, nil); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("the approval was used a second time: %v", err)
		}
		// Opening the drawer without a sale: the cashier cannot without approval; the manager opens it.
		drawer := func(token string) error {
			return call(ctx, http.MethodPost, token, gw+"/api/shift/drawer", map[string]string{"reason": "Bozuk para bozdurma"}, nil)
		}
		if err := drawer(cashierToken); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("the cashier opened the drawer without approval: %v", err)
		}
		if err := drawer(managerToken); err != nil {
			return "", err
		}
		drawerOpens++
		return fmt.Sprintf("%s was put in the register; the cashier taking money out 403; zero amount and empty description 422; the manager took 200.00 TL of interim cash;"+
			"the cashier took 50.00 TL with manager approval (wrong approval PIN and a second use of the approval 403); drawer opening without a sale: cashier 403, manager opened it", tl(cashIn)), nil
	}}
}

// shiftCloseScenario checks that Z is rejected with an open shift and the report (expected
// cash, cash shortage) of a shift closed with a blind count.
// The closing is requested right after the sale: staff must wait for the sale events
// to be processed.
func shiftCloseScenario(gw string) scenario {
	return scenario{"Shift closing: blind count, cash shortage; Z cannot be taken with an open shift; the report is printed", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		if err := call(ctx, http.MethodPost, managerToken, gw+"/api/reports/z", nil, nil); err == nil || !strings.Contains(err.Error(), "409") {
			return "", fmt.Errorf("z could be taken with an open shift: %v", err)
		}
		// 1.00 TL is counted short in the drawer.
		expected := openingCash + shiftCash + cashIn - cashOut
		var r struct {
			Shift             shift `json:"shift"`
			ReceiptCount      int64 `json:"receipt_count"`
			TotalKurus        int64 `json:"total_kurus"`
			ExpectedCashKurus int64 `json:"expected_cash_kurus"`
			DifferenceKurus   int64 `json:"difference_kurus"`
			DrawerOpens       int64 `json:"drawer_opens"`
		}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/shift/close", map[string]int64{"counted_cash_kurus": expected - 100}, &r); err != nil {
			return "", err
		}
		if r.ReceiptCount != shiftReceipts || r.ExpectedCashKurus != expected || r.DifferenceKurus != -100 || r.Shift.ClosedBy != "ayse" || r.DrawerOpens != drawerOpens {
			return "", fmt.Errorf("shift report: %d receipts, expected %s, difference %s; want %d receipts, expected %s, difference -1.00 TL",
				r.ReceiptCount, tl(r.ExpectedCashKurus), tl(r.DifferenceKurus), shiftReceipts, tl(expected))
		}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/module/baskets", nil, nil); err == nil || !strings.Contains(err.Error(), "409") {
			return "", fmt.Errorf("a basket could be opened in the closed shift: %v", err)
		}

		// History: the manager sees the newest shift and its report in the list; another
		// cashier cannot.
		var history []shift
		if err := call(ctx, http.MethodGet, managerToken, gw+"/api/shifts?limit=1", nil, &history); err != nil {
			return "", err
		}
		if len(history) != 1 || history[0].ID != r.Shift.ID {
			return "", fmt.Errorf("shift history: %+v; want newest shift no %d", history, r.Shift.ID)
		}
		var past struct {
			ExpectedCashKurus int64 `json:"expected_cash_kurus"`
		}
		reportURL := fmt.Sprintf("%s/api/shifts/%d/report", gw, r.Shift.ID)
		if err := call(ctx, http.MethodGet, managerToken, reportURL, nil, &past); err != nil {
			return "", err
		}
		if past.ExpectedCashKurus != r.ExpectedCashKurus {
			return "", fmt.Errorf("past report expected cash %s, at closing %s", tl(past.ExpectedCashKurus), tl(r.ExpectedCashKurus))
		}
		closedShift = closedShiftFacts{ID: r.Shift.ID, Expected: r.ExpectedCashKurus, Difference: r.DifferenceKurus}
		mert, err := login(ctx, gw, "mert", "2222")
		if err != nil {
			return "", err
		}
		if err := call(ctx, http.MethodGet, mert, reportURL, nil, nil); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("another cashier saw the shift report: %v", err)
		}
		if err := printVia(ctx, cashierToken, fmt.Sprintf("%s/api/shifts/%d/print", gw, r.Shift.ID)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Z with an open shift 409; %d receipts, sales %s, expected cash %s (including put in and taken out), counted %s, cash shortage %s; a sale after closing 409; past report the same, 403 for another cashier",
			r.ReceiptCount, tl(r.TotalKurus), tl(r.ExpectedCashKurus), tl(r.Shift.CountedCashKurus), tl(-r.DifferenceKurus)), nil
	}}
}

// login signs in as a cashier and returns the session key.
func login(ctx context.Context, gw, cashierID, pin string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := call(ctx, http.MethodPost, "", gw+"/api/auth/login", map[string]string{"cashier_id": cashierID, "pin": pin}, &out)
	return out.Token, err
}

// authScenario checks that sign-in and permissions are enforced at the gateway.
func authScenario(gw string) scenario {
	return scenario{"Cashier sign-in and authority", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		var cashiers []struct {
			ID string `json:"id"`
		}
		if err := call(ctx, http.MethodGet, "", gw+"/api/auth/cashiers", nil, &cashiers); err != nil {
			return "", err
		}
		if len(cashiers) < 2 {
			return "", fmt.Errorf("%d kasiyer listelendi", len(cashiers))
		}
		err := call(ctx, http.MethodPost, "", gw+"/api/auth/login", map[string]string{"cashier_id": "mert", "pin": "0000"}, nil)
		if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "deneme hakkı") {
			return "", fmt.Errorf("a wrong PIN was not rejected or the remaining attempts were not reported: %v", err)
		}
		if err := call(ctx, http.MethodPost, "", gw+"/api/module/baskets", nil, nil); err == nil || !strings.Contains(err.Error(), "401") {
			return "", fmt.Errorf("a basket could be opened without a sign-in: %v", err)
		}
		if err := call(ctx, http.MethodPost, cashierToken, gw+"/api/reports/z", nil, nil); err == nil || !strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("a cashier could take the end of day: %v", err)
		}
		// A sign-in with the right PIN resets the wrong-attempt counter.
		if _, err := login(ctx, gw, "mert", "2222"); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d cashiers; wrong PIN rejected (remaining attempts reported); request without a sign-in 401; the cashier's Z request 403", len(cashiers)), nil
	}}
}

// call sends a JSON request to the gateway; if token is not empty it carries the session
// key. It turns responses outside 2xx into an error.
func call(ctx context.Context, method, token, url string, body, out any) error {
	return callApproved(ctx, method, token, "", url, body, out)
}

// callApproved is like call; if approval is not empty it carries the manager approval.
func callApproved(ctx context.Context, method, token, approval, url string, body, out any) error {
	return callWith(ctx, http.DefaultClient, method, token, approval, url, body, out)
}

// callWith sends the request with the given client (e.g. one that carries the session cookie).
func callWith(ctx context.Context, client *http.Client, method, token, approval, url string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if approval != "" {
		req.Header.Set("X-Abarow-Approval", approval)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// The sales closed and cancelled in this run; the event and report
// checks look for them.
var (
	completedIDs, voidedIDs     []string
	completedTotal, voidedTotal int64
	// mealTotal is the total taken with the meal card (a separate line in Z).
	mealTotal int64
	// dayZNo is the Z number of the period this run's sales entered.
	dayZNo int64
)

func recordCompleted(id string, total int64) {
	completedIDs = append(completedIDs, id)
	completedTotal += total
}

func recordVoided(id string, total int64) {
	voidedIDs = append(voidedIDs, id)
	voidedTotal += total
}

// report is the gateway's X/Z report JSON.
type report struct {
	ZNo           int64      `json:"z_no"`
	ClosedAt      *time.Time `json:"closed_at"`
	ReceiptCount  int64      `json:"receipt_count"`
	TotalKurus    int64      `json:"total_kurus"`
	TaxTotalKurus int64      `json:"tax_total_kurus"`
	Payments      []struct {
		Method      string `json:"method"`
		AmountKurus int64  `json:"amount_kurus"`
	} `json:"payments"`
	VoidedCount      int64 `json:"voided_count"`
	VoidedTotalKurus int64 `json:"voided_total_kurus"`
}

// startDay waits until staff has processed the earlier events and opens a clean
// period for this run by taking a Z.
func startDay(ctx context.Context, gw, natsURL string) error {
	if err := staffCaughtUp(ctx, natsURL); err != nil {
		return err
	}
	var z report
	if err := call(ctx, http.MethodPost, managerToken, gw+"/api/reports/z", nil, &z); err != nil {
		return err
	}
	dayZNo = z.ZNo + 1
	return nil
}

// natsAuthScenario checks that NATS enforces identity and permission:
// a connection without an identity is rejected; the simulation tool's key (read-only)
// cannot publish a sale event, that is, a fake sale cannot enter the reports.
func natsAuthScenario(url string) scenario {
	return scenario{"NATS identity and permission: a connection without an identity and an unauthorized publish are rejected", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		if nc, err := nats.Connect(url, nats.MaxReconnects(0)); err == nil {
			nc.Close()
			return "", errors.New("a connection without an identity was accepted")
		} else if !strings.Contains(strings.ToLower(err.Error()), "authorization") {
			return "", fmt.Errorf("a connection without an identity was rejected with an unexpected error: %w", err)
		}

		denied := make(chan error, 1)
		seed := os.Getenv("ABAROW_NATS_NKEY")
		opt, err := nats.NkeyOptionFromSeed(seed)
		if err != nil {
			return "", err
		}
		nc, err := nats.Connect(url, opt, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case denied <- err:
			default:
			}
		}))
		if err != nil {
			return "", err
		}
		defer nc.Close()
		if err := nc.Publish("sale.completed.v1", []byte(`{"sale_id":"sahte"}`)); err != nil {
			return "", err
		}
		if err := nc.Flush(); err != nil {
			return "", err
		}
		select {
		case err := <-denied:
			if !errors.Is(err, nats.ErrPermissionViolation) {
				return "", fmt.Errorf("the publish was rejected with an unexpected error: %w", err)
			}
		case <-time.After(2 * time.Second):
			return "", errors.New("an unauthorized sale event publish was not rejected")
		}
		return "a connection without an identity was rejected; publishing sale.completed.v1 with a read-only key was rejected (only sales can publish)", nil
	}}
}

// connectNATS connects to NATS; if ABAROW_NATS_NKEY is given with the simulation
// tool's key (read permission only; it cannot publish).
func connectNATS(url string) (*nats.Conn, error) {
	var opts []nats.Option
	if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
		opt, err := nats.NkeyOptionFromSeed(seed)
		if err != nil {
			return nil, fmt.Errorf("could not read the NATS key: %w", err)
		}
		opts = append(opts, opt)
	}
	return nats.Connect(url, opts...)
}

// staffCaughtUp waits for staff to process all the sale events in the stream.
// If the stream does not exist yet there is no event to process.
func staffCaughtUp(ctx context.Context, natsURL string) error {
	nc, err := connectNATS(natsURL)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		cons, err := js.Consumer(ctx, salesStream, "staff")
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			return nil
		}
		if err == nil {
			var info *jetstream.ConsumerInfo
			if info, err = cons.Info(ctx); err == nil && info.NumPending == 0 && info.NumAckPending == 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("staff did not process the sale events: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// reportScenario checks that the X report holds this run's sales and that
// Z closes the day and opens a new period.
func reportScenario(gw, natsURL string) scenario {
	return scenario{"End of day: the X report holds the sales, Z closes the day", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		if err := staffCaughtUp(ctx, natsURL); err != nil {
			return "", err
		}
		var x report
		if err := call(ctx, http.MethodGet, managerToken, gw+"/api/reports/x", nil, &x); err != nil {
			return "", err
		}
		if x.ZNo != dayZNo || x.ReceiptCount != int64(len(completedIDs)) || x.TotalKurus != completedTotal ||
			x.VoidedCount != int64(len(voidedIDs)) || x.VoidedTotalKurus != voidedTotal {
			return "", fmt.Errorf("the X report: Z no %d, %d receipts %s, %d cancellations %s; want Z no %d, %d receipts %s, %d cancellations %s",
				x.ZNo, x.ReceiptCount, tl(x.TotalKurus), x.VoidedCount, tl(x.VoidedTotalKurus),
				dayZNo, len(completedIDs), tl(completedTotal), len(voidedIDs), tl(voidedTotal))
		}
		var paid int64
		for _, p := range x.Payments {
			paid += p.AmountKurus
		}
		if paid != x.TotalKurus {
			return "", fmt.Errorf("payments total %s, sales total %s", tl(paid), tl(x.TotalKurus))
		}
		var meal int64 = -1
		for _, p := range x.Payments {
			if p.Method == "meal_card" {
				meal = p.AmountKurus
			}
		}
		if meal != mealTotal {
			return "", fmt.Errorf("meal card %s in the X report, want %s", tl(meal), tl(mealTotal))
		}

		var z report
		if err := call(ctx, http.MethodPost, managerToken, gw+"/api/reports/z", nil, &z); err != nil {
			return "", err
		}
		if z.ZNo != x.ZNo || z.ReceiptCount != x.ReceiptCount || z.TotalKurus != x.TotalKurus || z.ClosedAt == nil {
			return "", fmt.Errorf("the Z report does not match the X report: %+v", z)
		}
		var next report
		if err := call(ctx, http.MethodGet, managerToken, gw+"/api/reports/x", nil, &next); err != nil {
			return "", err
		}
		if next.ZNo != z.ZNo+1 || next.ReceiptCount != 0 {
			return "", fmt.Errorf("the new period after Z did not open empty: %+v", next)
		}
		if err := printVia(ctx, managerToken, fmt.Sprintf("%s/api/reports/z/%d/print", gw, z.ZNo)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Z no %d: %d receipts, total %s, VAT %s, %d cancelled (%s); payment total equals sales; Z printed; new period Z no %d",
			z.ZNo, z.ReceiptCount, tl(z.TotalKurus), tl(z.TaxTotalKurus), z.VoidedCount, tl(z.VoidedTotalKurus), next.ZNo), nil
	}}
}

const salesStream = "ABAROW_SALES"

// staffStream is the stream of staff's shift events.
const staffStream = "ABAROW_STAFF"

// closedShiftFacts is the shift closed in this run (compared in the shift events
// check).
type closedShiftFacts struct{ ID, Expected, Difference int64 }

var closedShift closedShiftFacts

// staffEventsScenario checks that the events of the shift opened and closed in this run
// (shift.opened.v1, shift.closed.v1) reached the ABAROW_STAFF stream and that the
// closing event carries the cash difference.
func staffEventsScenario(url string) scenario {
	return scenario{"The shift events reached NATS JetStream", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		if closedShift.ID == 0 {
			return "", errors.New("no shift closed in this run")
		}
		nc, err := connectNATS(url)
		if err != nil {
			return "", err
		}
		defer nc.Close()
		js, err := jetstream.New(nc)
		if err != nil {
			return "", err
		}
		// The relay publishes the events at intervals.
		deadline := time.Now().Add(15 * time.Second)
		for {
			opened, closed, err := findShiftEvents(ctx, js, closedShift.ID)
			if err == nil && opened && closed != nil {
				r := closed.GetReport()
				if r.GetExpectedCashKurus() != closedShift.Expected || r.GetDifferenceKurus() != closedShift.Difference {
					return "", fmt.Errorf("closing event: expected %s, difference %s; in the report expected %s, difference %s",
						tl(r.GetExpectedCashKurus()), tl(r.GetDifferenceKurus()), tl(closedShift.Expected), tl(closedShift.Difference))
				}
				return fmt.Sprintf("shift no %d: shift.opened.v1 and shift.closed.v1 (protobuf) found; expected cash %s in the closing event, cash difference %s",
					closedShift.ID, tl(r.GetExpectedCashKurus()), tl(r.GetDifferenceKurus())), nil
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("the events of shift no %d did not come (opening %v, closing %v; last error: %v)", closedShift.ID, opened, closed != nil, err)
			}
			time.Sleep(time.Second)
		}
	}}
}

// findShiftEvents looks in the stream for the shift's opening and closing events.
func findShiftEvents(ctx context.Context, js jetstream.JetStream, shiftID int64) (bool, *staffv1.ShiftClosed, error) {
	s, err := js.Stream(ctx, staffStream)
	if err != nil {
		return false, nil, err
	}
	if s.CachedInfo().State.Msgs == 0 {
		return false, nil, nil
	}
	cons, err := js.OrderedConsumer(ctx, staffStream, jetstream.OrderedConsumerConfig{})
	if err != nil {
		return false, nil, err
	}
	opened := false
	var closed *staffv1.ShiftClosed
	for {
		batch, err := cons.Fetch(500, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			return opened, closed, err
		}
		n := 0
		for msg := range batch.Messages() {
			n++
			if !strings.HasPrefix(msg.Headers().Get("content-type"), "application/protobuf") {
				continue
			}
			switch msg.Subject() {
			case "shift.opened.v1":
				var e staffv1.ShiftOpened
				if proto.Unmarshal(msg.Data(), &e) == nil && e.GetShift().GetId() == shiftID {
					opened = true
				}
			case "shift.closed.v1":
				var e staffv1.ShiftClosed
				if proto.Unmarshal(msg.Data(), &e) == nil && e.GetReport().GetShift().GetId() == shiftID {
					closed = &e
				}
			}
			if meta, err := msg.Metadata(); err == nil && meta.NumPending == 0 {
				return opened, closed, nil
			}
		}
		if n == 0 {
			return opened, closed, batch.Error()
		}
	}
}

// eventsScenario checks that the events of this run's sales reached NATS
// JetStream. Since the relay publishes at intervals the events are waited for
// a while.
func eventsScenario(url string) scenario {
	return scenario{"The sale events reached NATS JetStream", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		nc, err := connectNATS(url)
		if err != nil {
			return "", err
		}
		defer nc.Close()
		js, err := jetstream.New(nc)
		if err != nil {
			return "", err
		}

		want := map[string]string{}
		for _, id := range completedIDs {
			want[id] = "sale.completed.v1"
		}
		for _, id := range voidedIDs {
			want[id] = "sale.voided.v1"
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			missing, err := findEvents(ctx, js, want)
			if len(missing) == 0 {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("%d events did not come or are not protobuf (last error: %v)", len(missing), err)
			}
			time.Sleep(time.Second)
		}
		stream, err := js.Stream(ctx, salesStream)
		if err != nil {
			return "", err
		}
		info, err := stream.Info(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d sale.completed + %d sale.voided events found (protobuf); the %s stream has %d messages in total",
			len(completedIDs), len(voidedIDs), salesStream, info.State.Msgs), nil
	}}
}

// eventSaleID reads the sale id in the event: sales publishes protobuf events
// (events.proto); old events left in the stream are JSON.
func eventSaleID(msg jetstream.Msg) (string, bool) {
	if strings.HasPrefix(msg.Headers().Get("content-type"), "application/protobuf") {
		switch msg.Subject() {
		case "sale.completed.v1":
			var e salesv1.SaleCompleted
			if proto.Unmarshal(msg.Data(), &e) != nil {
				return "", false
			}
			return e.GetSale().GetId(), true
		case "sale.voided.v1":
			var e salesv1.SaleVoided
			if proto.Unmarshal(msg.Data(), &e) != nil {
				return "", false
			}
			return e.GetSaleId(), true
		}
		return "", false
	}
	var p struct {
		SaleID string `json:"sale_id"`
	}
	if json.Unmarshal(msg.Data(), &p) != nil {
		return "", false
	}
	return p.SaleID, true
}

// findEvents reads the stream from the start and returns the sales not found. The events of
// this run must be protobuf (events.proto); an event published as JSON counts as
// not found.
func findEvents(ctx context.Context, js jetstream.JetStream, want map[string]string) (map[string]string, error) {
	missing := maps.Clone(want)
	cons, err := js.OrderedConsumer(ctx, salesStream, jetstream.OrderedConsumerConfig{})
	if err != nil {
		return missing, err
	}
	for {
		batch, err := cons.Fetch(500, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			return missing, err
		}
		n := 0
		for msg := range batch.Messages() {
			n++
			saleID, ok := eventSaleID(msg)
			if !ok {
				continue
			}
			subject, ok := missing[saleID]
			protobuf := strings.HasPrefix(msg.Headers().Get("content-type"), "application/protobuf")
			if ok && protobuf && msg.Subject() == subject && msg.Headers().Get("ce-type") == "com.abarow."+subject {
				delete(missing, saleID)
			}
		}
		if len(missing) == 0 {
			return missing, nil
		}
		if n == 0 {
			return missing, batch.Error()
		}
	}
}

// storeStream is the store stream on the store server fed from the registers' sales
// streams.
const storeStream = "ABAROW_STORE"

// storeScenario checks that the register's sale events reached the store server without loss
// or repetition, that no other register's event entered the register's stream, and that report
// sees the register as connected. The events are told apart by the CloudEvents headers: ce-id is
// the event's id, ce-source is "abarow/<store>/<register>".
func storeScenario(registerURL, storeURL, reportURL string) scenario {
	return scenario{"Store server: the sales reached the store without loss or repetition; the registers did not get mixed up", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		registerID := service.Env("ABAROW_REGISTER_ID", "register-1")
		source := "abarow/" + service.Env("ABAROW_STORE_ID", "store-1") + "/" + registerID
		own, completed, err := ownEvents(ctx, registerURL, source)
		if err != nil {
			return "", err
		}

		var opts []nats.Option
		if seed := os.Getenv("ABAROW_STORE_NATS_NKEY"); seed != "" {
			opt, err := nats.NkeyOptionFromSeed(seed)
			if err != nil {
				return "", fmt.Errorf("could not read the store server NATS key: %w", err)
			}
			opts = append(opts, opt)
		}
		bnc, err := nats.Connect(storeURL, opts...)
		if err != nil {
			return "", fmt.Errorf("operasyon NATS: %w", err)
		}
		defer bnc.Close()
		bjs, err := jetstream.New(bnc)
		if err != nil {
			return "", err
		}
		// If the source connection was just set up, because of NATS's retry interval the events
		// may take up to a minute to come.
		var stored map[string]int
		var arrived []event
		others := 0
		deadline := time.Now().Add(2 * time.Minute)
		for {
			events, err := readEvents(ctx, bjs, storeStream)
			arrived = events
			stored, others = map[string]int{}, 0
			for _, e := range events {
				if e.Source == source {
					stored[e.ID]++
				} else {
					others++
				}
			}
			missing := 0
			for id := range own {
				if stored[id] == 0 {
					missing++
				}
			}
			if err == nil && missing == 0 {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("%d of the %d events did not reach the store server (last error: %v)", len(own), missing, err)
			}
			time.Sleep(2 * time.Second)
		}
		// Each of the register's events comes with a sequence number (ce-sequence, ce-producer) and
		// for each sender the numbers are consecutive without gaps: headquarters understands a missing
		// record from this (pkg/sequence).
		numbered := map[string][]int64{}
		for _, e := range arrived {
			if e.Source == source && e.Producer != "" {
				numbered[e.Producer] = append(numbered[e.Producer], e.Seq)
			}
		}
		if len(numbered) == 0 {
			return "", fmt.Errorf("none of the events at the store server has a sequence number (ce-sequence)")
		}
		for producer, seqs := range numbered {
			slices.Sort(seqs)
			for i := 1; i < len(seqs); i++ {
				if seqs[i] != seqs[i-1]+1 {
					return "", fmt.Errorf("there is a gap in the sequence numbers of the %s events: %d then %d", producer, seqs[i-1], seqs[i])
				}
			}
		}
		for id, n := range stored {
			if n > 1 {
				return "", fmt.Errorf("olay %s operasyonda %d kez var", short(id), n)
			}
			if !own[id] {
				return "", fmt.Errorf("operasyonda bu kasadan, kasada olmayan olay var: %s", short(id))
			}
		}

		detail := fmt.Sprintf("all %d events at the register (sales and shifts) are at the store server exactly once; no other register's event in the register's streams; %d events from other registers at the store server",
			len(own), others)
		if reportURL != "" {
			// The report API is opened with the store password; a request without a sign-in is rejected.
			rc, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
			if err != nil {
				return "", err
			}
			call := func(ctx context.Context, method, _, url string, body, out any) error {
				return callWith(ctx, rc, method, "", "", url, body, out)
			}
			// report processes the events into its own database: all the register's receipts have
			// come, there is no missing receipt in between.
			type register struct {
				ID              string            `json:"id"`
				Link            string            `json:"link"`
				PendingEvents   uint64            `json:"pending_events"`
				ReceiptCount    int64             `json:"receipt_count"`
				MissingReceipts []json.RawMessage `json:"missing_receipts"`
				MissingEvents   []json.RawMessage `json:"missing_events"`
				LastClosedShift *struct {
					ShiftID         int64 `json:"shift_id"`
					DifferenceKurus int64 `json:"difference_kurus"`
				} `json:"last_closed_shift"`
			}
			// The shift closed in this run shows with its cash difference.
			shiftShown := func(r register) bool {
				return closedShift.ID == 0 || (r.LastClosedShift != nil && r.LastClosedShift.ShiftID == closedShift.ID &&
					r.LastClosedShift.DifferenceKurus == closedShift.Difference)
			}
			var st struct {
				Registers []register `json:"registers"`
			}
			var mine register
			deadline := time.Now().Add(30 * time.Second)
			for {
				err := call(ctx, http.MethodGet, "", reportURL+"/api/registers", nil, &st)
				for _, r := range st.Registers {
					if r.ID == registerID {
						mine = r
					}
				}
				if err == nil && mine.Link == "connected" && mine.PendingEvents == 0 && mine.ReceiptCount == completed && shiftShown(mine) {
					break
				}
				if time.Now().After(deadline) {
					return "", fmt.Errorf("report: register %+v (last shift %+v); it should show as connected, with no pending events, with %d receipts and a cash difference for shift no %d (%v)",
						mine, mine.LastClosedShift, completed, closedShift.ID, err)
				}
				time.Sleep(time.Second)
			}
			if len(mine.MissingReceipts) > 0 {
				return "", fmt.Errorf("report shows missing receipts: %s", mine.MissingReceipts)
			}
			if len(mine.MissingEvents) > 0 {
				return "", fmt.Errorf("report shows missing records (a gap in the sequence numbers): %s", mine.MissingEvents)
			}
			// The daily store report includes this run's sales; the register
			// totals equal the report's total.
			var day struct {
				ReceiptCount int64 `json:"receipt_count"`
				TotalKurus   int64 `json:"total_kurus"`
				Payments     []struct {
					AmountKurus int64 `json:"amount_kurus"`
				} `json:"payments"`
				Registers []struct {
					ID           string `json:"id"`
					ReceiptCount int64  `json:"receipt_count"`
					TotalKurus   int64  `json:"total_kurus"`
				} `json:"registers"`
			}
			if err := call(ctx, http.MethodGet, "", reportURL+"/api/reports/summary", nil, &day); err != nil {
				return "", fmt.Errorf("daily report: %w", err)
			}
			var regReceipts, regTotal, paid int64
			var mineReceipts, mineTotal int64
			for _, r := range day.Registers {
				regReceipts += r.ReceiptCount
				regTotal += r.TotalKurus
				if r.ID == registerID {
					mineReceipts, mineTotal = r.ReceiptCount, r.TotalKurus
				}
			}
			for _, p := range day.Payments {
				paid += p.AmountKurus
			}
			if regReceipts != day.ReceiptCount || regTotal != day.TotalKurus || paid != day.TotalKurus {
				return "", fmt.Errorf("daily report is inconsistent: %d receipts %s; registers %d receipts %s; payments %s",
					day.ReceiptCount, tl(day.TotalKurus), regReceipts, tl(regTotal), tl(paid))
			}
			if mineReceipts < int64(len(completedIDs)) || mineTotal < completedTotal {
				return "", fmt.Errorf("in the daily report this register has %d receipts %s; in this run there should be at least %d receipts %s",
					mineReceipts, tl(mineTotal), len(completedIDs), tl(completedTotal))
			}
			detail += fmt.Sprintf("; report rejected the request without a sign-in; it shows the register as connected, with %d receipts and no missing receipts; last shift no %d, cash difference %s; daily store report %d receipts %s (this register %d receipts %s), payment total equals sales",
				mine.ReceiptCount, closedShift.ID, tl(closedShift.Difference), day.ReceiptCount, tl(day.TotalKurus), mineReceipts, tl(mineTotal))
		}
		return detail, nil
	}}
}

// ownEvents reads the ids of the events in the register's streams (sales and shifts) and
// the number of completed sales; it checks that all of them came from this register
// (source).
func ownEvents(ctx context.Context, registerURL, source string) (map[string]bool, int64, error) {
	nc, err := connectNATS(registerURL)
	if err != nil {
		return nil, 0, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, 0, err
	}
	local, err := readEvents(ctx, js, salesStream)
	if err != nil {
		return nil, 0, fmt.Errorf("could not read the register's stream: %w", err)
	}
	// The shift events go to the tier above too (the stream is set up with the first shift).
	shifts, err := readEvents(ctx, js, staffStream)
	if err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, 0, fmt.Errorf("could not read the register's shift stream: %w", err)
	}
	local = append(local, shifts...)
	own := map[string]bool{}
	var completed int64
	for _, e := range local {
		if e.Source != source {
			return nil, 0, fmt.Errorf("there is an event from another source in the register's stream: %s (%s)", e.Source, short(e.ID))
		}
		own[e.ID] = true
		if e.Type == "com.abarow.sale.completed.v1" {
			completed++
		}
	}
	if len(own) == 0 {
		return nil, 0, errors.New("no events at the register")
	}
	return own, completed, nil
}

// headquartersScenario checks from ingest's store state that the register's events
// reached the back office (headquarters) through the store server and were written to PostgreSQL:
// the register's event and sale counts must be exactly the same.
func headquartersScenario(registerURL, ingestURL string) scenario {
	return scenario{"Headquarters: the register's events reached headquarters without loss or repetition", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		storeID := service.Env("ABAROW_STORE_ID", "store-1")
		registerID := service.Env("ABAROW_REGISTER_ID", "register-1")
		own, completed, err := ownEvents(ctx, registerURL, "abarow/"+storeID+"/"+registerID)
		if err != nil {
			return "", err
		}
		type register struct {
			ID         string `json:"id"`
			EventCount int64  `json:"event_count"`
			SaleCount  int64  `json:"sale_count"`
		}
		type store struct {
			ID         string     `json:"id"`
			Link       string     `json:"link"`
			EventCount int64      `json:"event_count"`
			SaleCount  int64      `json:"sale_count"`
			Registers  []register `json:"registers"`
			// MissingEvents are records that never reached headquarters (a gap in the sequence numbers).
			MissingEvents []json.RawMessage `json:"missing_events"`
		}
		var mine store
		var reg register
		// If the store server's source was just connected, because of NATS's retry
		// interval the events may take up to a minute to come.
		deadline := time.Now().Add(2 * time.Minute)
		for {
			var stores []store
			err := call(ctx, http.MethodGet, "", ingestURL+"/api/stores", nil, &stores)
			for _, s := range stores {
				if s.ID == storeID {
					mine = s
				}
			}
			reg = register{}
			for _, r := range mine.Registers {
				if r.ID == registerID {
					reg = r
				}
			}
			if err == nil && mine.Link == "connected" && reg.EventCount == int64(len(own)) && reg.SaleCount == completed {
				break
			}
			if reg.EventCount > int64(len(own)) || reg.SaleCount > completed {
				return "", fmt.Errorf("there are more records at headquarters than from this register: %d events %d sales; at the register %d events %d sales", reg.EventCount, reg.SaleCount, len(own), completed)
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("headquarters: store %+v; it should be connected and %d events, %d sales from this register should show (%v)", mine, len(own), completed, err)
			}
			time.Sleep(2 * time.Second)
		}
		if len(mine.MissingEvents) > 0 {
			return "", fmt.Errorf("headquarters shows missing records (a gap in the sequence numbers): %s", mine.MissingEvents)
		}
		return fmt.Sprintf("the %d events and %d sales at the register are identical at headquarters, no gaps in the sequence numbers; store %s connected, in total %d events, %d sales, %d registers at headquarters",
			len(own), completed, storeID, mine.EventCount, mine.SaleCount, len(mine.Registers)), nil
	}}
}

// ledgerView is the compared figures of a sales report (the back
// house's day report and headquarters' summary use the same field names).
type ledgerView struct {
	Date                                      string
	Receipts, Total, Tax, Voided, VoidedTotal int64
	Payments                                  map[string]int64
	Taxes                                     map[int64][2]int64
	Cashiers                                  map[string][4]int64
}

type ledgerJSON struct {
	From             string
	ReceiptCount     int64 `json:"receipt_count"`
	TotalKurus       int64 `json:"total_kurus"`
	TaxTotalKurus    int64 `json:"tax_total_kurus"`
	VoidedCount      int64 `json:"voided_count"`
	VoidedTotalKurus int64 `json:"voided_total_kurus"`
	Payments         []struct {
		Method      string `json:"method"`
		AmountKurus int64  `json:"amount_kurus"`
	} `json:"payments"`
	Taxes []struct {
		RateBp     int64 `json:"rate_bp"`
		GrossKurus int64 `json:"gross_kurus"`
		TaxKurus   int64 `json:"tax_kurus"`
	} `json:"taxes"`
	Cashiers []struct {
		StoreID          string `json:"store_id"`
		CashierID        string `json:"cashier_id"`
		ReceiptCount     int64  `json:"receipt_count"`
		TotalKurus       int64  `json:"total_kurus"`
		VoidedCount      int64  `json:"voided_count"`
		VoidedTotalKurus int64  `json:"voided_total_kurus"`
	} `json:"cashiers"`
}

func (j ledgerJSON) view(date string) ledgerView {
	v := ledgerView{Date: date, Receipts: j.ReceiptCount, Total: j.TotalKurus, Tax: j.TaxTotalKurus, Voided: j.VoidedCount,
		VoidedTotal: j.VoidedTotalKurus, Payments: map[string]int64{}, Taxes: map[int64][2]int64{}, Cashiers: map[string][4]int64{}}
	for _, p := range j.Payments {
		v.Payments[p.Method] = p.AmountKurus
	}
	for _, t := range j.Taxes {
		v.Taxes[t.RateBp] = [2]int64{t.GrossKurus, t.TaxKurus}
	}
	for _, c := range j.Cashiers {
		v.Cashiers[c.CashierID] = [4]int64{c.ReceiptCount, c.TotalKurus, c.VoidedCount, c.VoidedTotalKurus}
	}
	return v
}

// ledgerScenario compares the store server's report for today with headquarters' summary
// for the same day and store: both come from the sales ledger (pkg/ledger); the receipt,
// total, VAT, cancellation, payment, VAT breakdown and cashier figures must be exactly the
// same. Headquarters may take the events a little behind; it is waited for a while.
func ledgerScenario(reportURL, reportingURL string) scenario {
	return scenario{"Sales ledger: the store server's day report and headquarters' summary are the same", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		storeID := service.Env("ABAROW_STORE_ID", "store-1")
		store, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
		if err != nil {
			return "", err
		}
		jar, err := cookiejar.New(nil)
		if err != nil {
			return "", err
		}
		headquarters := &http.Client{Jar: jar}
		if err := callWith(ctx, headquarters, http.MethodPost, "", "", reportingURL+"/api/auth/login",
			map[string]string{"username": os.Getenv("ABAROW_HEADQUARTERS_USER"), "password": os.Getenv("ABAROW_HEADQUARTERS_PASSWORD")}, nil); err != nil {
			return "", fmt.Errorf("headquarters sign-in: %w", err)
		}
		var local, central ledgerView
		deadline := time.Now().Add(time.Minute)
		for {
			var day, sum ledgerJSON
			if err := callWith(ctx, store, http.MethodGet, "", "", reportURL+"/api/reports/summary", nil, &day); err != nil {
				return "", fmt.Errorf("store server day report: %w", err)
			}
			q := url.Values{"from": {day.From}, "to": {day.From}, "store": {storeID}}
			if err := callWith(ctx, headquarters, http.MethodGet, "", "", reportingURL+"/api/reports/summary?"+q.Encode(), nil, &sum); err != nil {
				return "", fmt.Errorf("headquarters summary: %w", err)
			}
			local, central = day.view(day.From), sum.view(sum.From)
			if fmt.Sprint(local) == fmt.Sprint(central) {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("the figures differ:\n    store server %+v\n    headquarters %+v", local, central)
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Sprintf("%s day, %s: %d receipts %s, VAT %s, %d cancelled; payments, the VAT breakdown and the figures of %d cashiers are the same in both tiers",
			local.Date, storeID, local.Receipts, tl(local.Total), tl(local.Tax), local.Voided, len(local.Cashiers)), nil
	}}
}

// headquartersCatalogScenario checks that a product added at headquarters (spec: barcode:price in kuruş)
// came down to the register's catalog through headquarters → store server → register.
func headquartersCatalogScenario(addr, spec string) scenario {
	return scenario{"Headquarters' catalog came down to the register", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		barcode, priceText, ok := strings.Cut(spec, ":")
		price, err := strconv.ParseInt(priceText, 10, 64)
		if !ok || err != nil {
			return "", fmt.Errorf("ABAROW_HEADQUARTERS_PRODUCT must be barcode:price (%q)", spec)
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return "", err
		}
		defer conn.Close()
		catalog := catalogv1.NewCatalogServiceClient(conn)
		// If the stream sources were just connected, because of NATS's retry interval the
		// product may take a few minutes to come down.
		start := time.Now()
		deadline := start.Add(4 * time.Minute)
		for {
			resp, err := catalog.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: barcode})
			if err == nil && resp.GetProduct().GetUnitPriceKurus() == price {
				return fmt.Sprintf("%s (%s) is in the register's catalog with %s; it came down in %s",
					resp.GetProduct().GetName(), barcode, tl(price), time.Since(start).Round(time.Second)), nil
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("headquarters' product %s did not come down to the register: %v %v", barcode, resp.GetProduct(), err)
			}
			time.Sleep(2 * time.Second)
		}
	}}
}

// storePriceScenario gives a product a store price through the store server screen's API
// (spec: product:barcode:price in kuruş) and checks that the price came down to the register's
// catalog and takes precedence over the headquarters price.
func storePriceScenario(catalogAddr, reportURL, spec string) scenario {
	return scenario{"The store price came down to the register", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		parts := strings.Split(spec, ":")
		if len(parts) != 3 {
			return "", fmt.Errorf("ABAROW_STORE_PRICE must be product:barcode:price (%q)", spec)
		}
		productID, barcode := parts[0], parts[1]
		price, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return "", err
		}
		rc, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
		if err != nil {
			return "", err
		}
		// It waits until the product comes from headquarters to the store server.
		var product struct {
			Product struct {
				HeadquartersPriceKurus int64 `json:"headquarters_price_kurus"`
			} `json:"product"`
		}
		deadline := time.Now().Add(3 * time.Minute)
		for {
			err := callWith(ctx, rc, http.MethodGet, "", "", reportURL+"/api/prices/"+productID, nil, &product)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("product %s did not reach the store server: %v", productID, err)
			}
			time.Sleep(2 * time.Second)
		}
		if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/prices/"+productID, map[string]int64{"unit_price_kurus": price}, nil); err != nil {
			return "", fmt.Errorf("the store price could not be given: %w", err)
		}
		conn, err := grpc.NewClient(catalogAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return "", err
		}
		defer conn.Close()
		catalog := catalogv1.NewCatalogServiceClient(conn)
		start := time.Now()
		deadline = start.Add(3 * time.Minute)
		for {
			resp, err := catalog.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: barcode})
			if err == nil && resp.GetProduct().GetUnitPriceKurus() == price {
				return fmt.Sprintf("%s: headquarters price %s, store price %s; the store price is in effect at the register (came down in %s)",
					resp.GetProduct().GetName(), tl(product.Product.HeadquartersPriceKurus), tl(price), time.Since(start).Round(time.Second)), nil
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("the store price did not come down to the register: %v %v", resp.GetProduct(), err)
			}
			time.Sleep(2 * time.Second)
		}
	}}
}

// storeCashierScenario adds a cashier through the store server screen's API
// (if left from a previous run it gives a new PIN and opens the account), checks that the
// cashier came down to the register and signed in at the register, that sign-in was rejected when
// the account was closed and that the open session closed.
// stockItem is a product in the store server's stock list; the quantity is per
// place (the stock service is the same as headquarters').
type stockItem struct {
	ProductID string           `json:"product_id"`
	Name      string           `json:"name"`
	QtyMilli  map[string]int64 `json:"qty_milli"`
}

// stockScenario checks, with a product specific to the register (two registers run at the same time),
// that a sale is deducted from store stock and that goods receipt and count correct
// the stock.
func stockScenario(reportURL string) scenario {
	return scenario{"Store stock: a sale deducts, goods receipt and count correct", func(ctx context.Context, c salesv1.SalesServiceClient) (string, error) {
		rc, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
		if err != nil {
			return "", err
		}
		reg := service.Env("ABAROW_REGISTER_ID", "register-1")
		product := "stok-" + reg
		name := "Stok Denemesi " + reg
		read := func() (int64, bool, error) {
			var out struct {
				Here  string
				Items []stockItem
			}
			if err := callWith(ctx, rc, http.MethodGet, "", "", reportURL+"/api/stock?q="+url.QueryEscape(name), nil, &out); err != nil {
				return 0, false, err
			}
			for _, it := range out.Items {
				if it.ProductID == product {
					return it.QtyMilli[out.Here], true, nil
				}
			}
			return 0, false, nil
		}
		// waitFor waits for the stock to be want (the sale comes through the message path).
		waitFor := func(want int64) error {
			deadline := time.Now().Add(30 * time.Second)
			for {
				got, _, err := read()
				if err == nil && got == want {
					return nil
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("stock of %s is %d, want %d (%v)", product, got, want, err)
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		before, _, err := read()
		if err != nil {
			return "", err
		}
		line := &salesv1.SaleLine{ProductId: product, Name: name, QtyMilli: 2000, UnitPriceKurus: 1000, TaxRateBp: 2000}
		if _, err := sell(ctx, c, []*salesv1.SaleLine{line}, cash(2000)); err != nil {
			return "", err
		}
		if err := waitFor(before - 2000); err != nil {
			return "", fmt.Errorf("the sale did not deduct from stock: %w", err)
		}
		ref := fmt.Sprintf("SIM-%s-%d", reg, time.Now().UnixNano())
		receipt := map[string]any{"supplier": "Simülasyon Tedarik", "ref": ref, "lines": []map[string]any{{"product_id": product, "qty_milli": 5000}}}
		if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/stock/receipts", receipt, nil); err != nil {
			return "", fmt.Errorf("mal kabul: %w", err)
		}
		if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/stock/receipts", receipt, nil); err == nil || !strings.Contains(err.Error(), "409") {
			return "", fmt.Errorf("the same delivery note was accepted a second time: %v", err)
		}
		if err := waitFor(before + 3000); err != nil {
			return "", fmt.Errorf("the goods receipt did not enter stock: %w", err)
		}
		count := map[string]any{"ref": "SAYIM-" + ref, "lines": []map[string]any{{"product_id": product, "counted_milli": 1000}}}
		if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/stock/counts", count, nil); err != nil {
			return "", fmt.Errorf("count: %w", err)
		}
		if err := waitFor(1000); err != nil {
			return "", fmt.Errorf("the count did not correct the stock: %w", err)
		}
		return fmt.Sprintf("%s: sale −2, goods receipt +5 (the same delivery note was rejected), the count corrected it to 1 unit", product), nil
	}}
}

func storeCashierScenario(gw, reportURL, id string) scenario {
	return scenario{"A cashier managed on the store server came down to the register", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		rc, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
		if err != nil {
			return "", err
		}
		pin := fmt.Sprintf("%06d", rand.IntN(1000000))
		base := reportURL + "/api/cashiers"
		err = callWith(ctx, rc, http.MethodPost, "", "", base, map[string]string{"id": id, "name": "Operasyon Kasiyeri " + id, "role": "cashier", "pin": pin}, nil)
		if err != nil && strings.Contains(err.Error(), "409") {
			if err = callWith(ctx, rc, http.MethodPost, "", "", base+"/"+id+"/pin", map[string]string{"pin": pin}, nil); err == nil {
				err = callWith(ctx, rc, http.MethodPost, "", "", base+"/"+id+"/disabled", map[string]bool{"disabled": false}, nil)
			}
		}
		if err != nil {
			return "", fmt.Errorf("kasiyer operasyonda eklenemedi: %w", err)
		}
		start := time.Now()
		deadline := start.Add(3 * time.Minute)
		var token string
		for {
			if token, err = login(ctx, gw, id, pin); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("a cashier added on the store server could not sign in at the register: %w", err)
			}
			time.Sleep(time.Second)
		}
		arrived := time.Since(start).Round(time.Second)

		if err := callWith(ctx, rc, http.MethodPost, "", "", base+"/"+id+"/disabled", map[string]bool{"disabled": true}, nil); err != nil {
			return "", fmt.Errorf("the account could not be closed: %w", err)
		}
		start = time.Now()
		deadline = start.Add(3 * time.Minute)
		for {
			sessionErr := call(ctx, http.MethodGet, token, gw+"/api/auth/me", nil, nil)
			_, loginErr := login(ctx, gw, id, pin)
			if sessionErr != nil && strings.Contains(sessionErr.Error(), "401") && loginErr != nil && strings.Contains(loginErr.Error(), "403") {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("a cashier whose account was closed can be used at the register: session %v, sign-in %v", sessionErr, loginErr)
			}
			time.Sleep(time.Second)
		}
		var cashiers []struct {
			ID string `json:"id"`
		}
		if err := call(ctx, http.MethodGet, "", gw+"/api/auth/cashiers", nil, &cashiers); err != nil {
			return "", err
		}
		for _, c := range cashiers {
			if c.ID == id {
				return "", fmt.Errorf("a cashier whose account was closed is in the register's sign-in list")
			}
		}
		return fmt.Sprintf("%s was added, signed in at the register in %s; when the account was closed the session closed in %s, sign-in was rejected (403)",
			id, arrived, time.Since(start).Round(time.Second)), nil
	}}
}

// reportLogin checks that a request without a sign-in to the store server report API
// is rejected, signs in with the store password and returns a client that carries the
// session cookie. If the store server has a manager with an open account (e.g. the
// simulation is running again in the same environment; the screen test added a manager) the store
// password is off; then the trial manager is used to sign in
// (ABAROW_STORE_REPORT_MANAGER, code:PIN).
func reportLogin(ctx context.Context, reportURL, password string) (*http.Client, error) {
	if err := call(ctx, http.MethodGet, "", reportURL+"/api/registers", nil, nil); err == nil || !strings.Contains(err.Error(), "401") {
		return nil, fmt.Errorf("the report API did not reject a request without a sign-in: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	rc := &http.Client{Jar: jar}
	err = callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/auth/login", map[string]string{"password": password}, nil)
	if err != nil && strings.Contains(err.Error(), "store password off") {
		id, pin, _ := strings.Cut(os.Getenv("ABAROW_STORE_REPORT_MANAGER"), ":")
		err = callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/auth/login", map[string]string{"cashier_id": id, "pin": pin}, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("store server sign-in: %w", err)
	}
	return rc, nil
}

// event is the id, source and kind of an event in the stream (CloudEvents
// headers).
// event is the id, source and the sender outbox's sequence information of an event in the stream
// (ce-producer, ce-sequence; pkg/sequence).
type event struct {
	ID, Source, Type, Producer string
	Seq                        int64
}

// readEvents reads the ids and sources of all the events in the stream.
func readEvents(ctx context.Context, js jetstream.JetStream, stream string) ([]event, error) {
	s, err := js.Stream(ctx, stream)
	if err != nil {
		return nil, err
	}
	if s.CachedInfo().State.Msgs == 0 {
		return nil, nil
	}
	cons, err := js.OrderedConsumer(ctx, stream, jetstream.OrderedConsumerConfig{})
	if err != nil {
		return nil, err
	}
	var events []event
	for {
		batch, err := cons.Fetch(500, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			return events, err
		}
		n := 0
		for msg := range batch.Messages() {
			n++
			ev := event{ID: msg.Headers().Get("ce-id"), Source: msg.Headers().Get("ce-source"), Type: msg.Headers().Get("ce-type")}
			if producer, seq, ok := sequence.FromHeaders(msg.Headers()); ok {
				ev.Producer, ev.Seq = producer, seq
			}
			events = append(events, ev)
			if meta, err := msg.Metadata(); err == nil && meta.NumPending == 0 {
				return events, nil
			}
		}
		if n == 0 {
			return events, fmt.Errorf("%s okunurken durdu: %v", stream, batch.Error())
		}
	}
}

func describe(s *salesv1.Sale) string {
	return fmt.Sprintf("receipt no %d, fiscal no %s, total %s, VAT %s, change %s",
		s.GetReceiptNo(), s.GetFiscalNo(), tl(s.GetTotalKurus()), tl(s.GetTaxTotalKurus()), tl(s.GetChangeKurus()))
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// storeCatalogScenario tries product management at the store server: while headquarters
// counts as connected a product cannot be added (403); when the manager closes the connection the
// product added comes down to the register's catalog; when reconnected writing closes again. If
// headquarters is not installed (a store without headquarters) a product can be added from scratch.
func storeCatalogScenario(catalogAddr, reportURL string) scenario {
	return scenario{"Product management on the store server: read-only while connected, comes down to the register when disconnected", func(ctx context.Context, _ salesv1.SalesServiceClient) (string, error) {
		rc, err := reportLogin(ctx, reportURL, os.Getenv("ABAROW_STORE_REPORT_PASSWORD"))
		if err != nil {
			return "", err
		}
		type office struct {
			Installed       bool `json:"installed"`
			Connected       bool `json:"connected"`
			CatalogWritable bool `json:"catalog_writable"`
		}
		var st office
		if err := callWith(ctx, rc, http.MethodGet, "", "", reportURL+"/api/settings/headquarters", nil, &st); err != nil {
			return "", fmt.Errorf("could not read the setting: %w", err)
		}
		reg := service.Env("ABAROW_REGISTER_ID", "register-1")
		code := "store-yerel-" + reg
		barcode := fmt.Sprintf("29900%08d", time.Now().UnixNano()%100000000)
		product := map[string]any{"code": code, "name": "Mağaza Ürünü " + reg, "unit": "piece", "tax_rate_bp": 2000, "barcodes": []string{barcode}, "unit_price_kurus": 777}
		note := "store without headquarters: a product can be added from scratch"
		if st.Installed {
			if !st.Connected || st.CatalogWritable {
				return "", fmt.Errorf("a store with headquarters should count as connected at the start: %+v", st)
			}
			if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/catalog/products", product, nil); err == nil || !strings.Contains(err.Error(), "403") {
				return "", fmt.Errorf("a product was added while connected: %v", err)
			}
			// Reading is open.
			if err := callWith(ctx, rc, http.MethodGet, "", "", reportURL+"/api/catalog/products", nil, nil); err != nil {
				return "", fmt.Errorf("the catalog could not be read while connected: %w", err)
			}
			if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/settings/headquarters", map[string]bool{"connected": false}, &st); err != nil {
				return "", fmt.Errorf("could not disconnect: %w", err)
			}
			if st.Connected || !st.CatalogWritable {
				return "", fmt.Errorf("disconnected but the state is %+v", st)
			}
			note = "403 while connected, added when disconnected"
		} else if st.Connected || !st.CatalogWritable {
			return "", fmt.Errorf("a store without headquarters should be disconnected and writable: %+v", st)
		}
		if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/catalog/products", product, nil); err != nil {
			return "", fmt.Errorf("could not add the product: %w", err)
		}

		conn, err := grpc.NewClient(catalogAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return "", err
		}
		defer conn.Close()
		catalog := catalogv1.NewCatalogServiceClient(conn)
		start := time.Now()
		deadline := start.Add(2 * time.Minute)
		for {
			resp, err := catalog.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: barcode})
			if err == nil && resp.GetProduct().GetUnitPriceKurus() == 777 {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("the product %s added at the store did not come down to the register: %v", barcode, err)
			}
			time.Sleep(time.Second)
		}
		landed := time.Since(start).Round(100 * time.Millisecond)

		if st.Installed {
			if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/settings/headquarters", map[string]bool{"connected": true}, &st); err != nil || !st.Connected {
				return "", fmt.Errorf("could not reconnect: %v %+v", err, st)
			}
			if err := callWith(ctx, rc, http.MethodPost, "", "", reportURL+"/api/catalog/products/"+code+"/discontinue", map[string]any{"version": 1, "discontinued": true}, nil); err == nil || !strings.Contains(err.Error(), "403") {
				return "", fmt.Errorf("writing did not close when reconnected: %v", err)
			}
		}
		return fmt.Sprintf("%s (%s): %s; it came down to the register's catalog in %s", code, barcode, note, landed), nil
	}}
}
