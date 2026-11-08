// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the screen's single entry point. It validates the cashier session and,
// for sale requests, the cashier's open shift; it translates the core operations
// (payment, cancellation, shift, reports) into calls to the relevant services;
// it forwards every other request to the active vertical module. It holds no business rules.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/login"
	"abarow.com/pkg/paymethod"
	"abarow.com/pkg/service"
)

// CashierHeader is the id of the cashier in the session on requests forwarded to the
// module. Gateway puts it; a value sent by the client is deleted.
const CashierHeader = "X-Abarow-Cashier-Id"

// CashierNameHeader is the name of the cashier in the session (URL-encoded; in a
// restaurant the waiter's name on the kitchen ticket and the check). Gateway puts it.
const CashierNameHeader = "X-Abarow-Cashier-Name"

// ApprovalHeader is the manager approval key on requests that need manager authority
// (POST /api/auth/approve). Gateway passes it to staff; staff checks the authority.
const ApprovalHeader = "X-Abarow-Approval"

// DefaultCheckoutTimeout is the default upper limit of the payment request: a card payment
// (payment, 90 s) + the fiscal record (fiscal, 30 s) + writing the result (sales, at most
// 30 s) fit, yet it does not hang forever.
const DefaultCheckoutTimeout = 3 * time.Minute

// Config is the install information the screen needs.
type Config struct {
	// Module is the active vertical module (e.g. "retail").
	Module string `json:"module"`
	// If FiscalMode is "simulator" the screen shows the warning about receipts with no
	// fiscal value on every page.
	FiscalMode string `json:"fiscal_mode"`
	StoreID    string `json:"store_id"`
	RegisterID string `json:"register_id"`
	// Update is the handler that goes to this register's update agent's status API
	// (e.g. a reverse proxy); if nil update is not installed. It asks for a cashier session.
	Update http.Handler `json:"-"`
	// CheckoutTimeout is the upper limit of the payment request; DefaultCheckoutTimeout if zero.
	// The limit must be longer than the total of sales' sub-calls (card payment 90 s
	// + fiscal record 30 s + database write); otherwise the gate returns an error
	// to the cashier while sales is still working.
	CheckoutTimeout time.Duration `json:"-"`
	// UpdateSecret is the secret key shared with the agent (ABAROW_UPDATE_APPROVE_SECRET):
	// it is added to the approval requests; the agent's "safe moment" question must carry it too.
	UpdateSecret string `json:"-"`
}

// New returns the gateway handler. module is the handler that goes to the active
// vertical module (e.g. a reverse proxy). If device is nil (no ABAROW_DEVICE_ADDR)
// the barcode reader flow is off.
// maxJSON is the largest JSON request body these endpoints read (their bodies are a few fields).
const maxJSON = 16 << 10

func New(sales salesv1.SalesServiceClient, staff staffv1.StaffServiceClient, device devicev1.DeviceServiceClient, module http.Handler, cfg Config) http.Handler {
	h := &handler{sales: sales, staff: staff, device: device, cfg: cfg}
	mux := http.NewServeMux()

	// Open to everyone: the install information and what the sign-in screen needs.
	mux.HandleFunc("GET /api/config", h.config)
	mux.HandleFunc("GET /api/auth/cashiers", h.cashiers)
	mux.HandleFunc("POST /api/auth/login", h.login)

	// Kasiyer oturumu gerekenler.
	mux.Handle("GET /api/auth/me", h.authed(h.me))
	// What the barcode reader reads (the stream) and imitating a scan on the simulator.
	mux.Handle("GET /api/device/scans", h.authed(h.scans))
	mux.Handle("POST /api/device/scans", h.authed(h.simulateScan))
	mux.Handle("POST /api/device/printer", h.authed(h.simulatePrinter))
	mux.Handle("POST /api/device/weight", h.authed(h.simulateWeight))
	mux.Handle("POST /api/auth/logout", h.authed(h.logout))
	mux.Handle("POST /api/auth/approve", h.authed(h.approve))
	mux.Handle("GET /api/shift", h.authed(h.currentShift))
	mux.Handle("POST /api/shift/open", h.authed(h.openShift))
	mux.Handle("POST /api/shift/close", h.authed(h.closeShift))
	mux.Handle("POST /api/shift/cash", h.authed(h.cashMovement))
	mux.Handle("POST /api/shift/drawer", h.authed(h.openDrawer))
	mux.Handle("GET /api/shifts", h.authed(h.listShifts))
	mux.Handle("GET /api/shifts/{id}/report", h.authed(h.shiftReport))
	mux.Handle("GET /api/reports/x", h.authed(h.xReport))
	mux.Handle("POST /api/reports/x/print", h.authed(h.printDay))
	mux.Handle("POST /api/reports/z/{zno}/print", h.authed(h.printDay))
	mux.Handle("POST /api/shifts/{id}/print", h.authed(h.printShift))
	mux.Handle("POST /api/reports/z", h.authed(h.closeDay))

	// The update agent's status and approval code (a cashier session is enough; no shift is needed).
	if cfg.Update != nil {
		mux.Handle("GET /api/update/status", h.authed(cfg.Update.ServeHTTP))
		// Approving, postponing and skipping the new version needs manager approval.
		mux.Handle("POST /api/update/{op}", h.authed(h.updateWrite))
		// The agent's "is it a safe moment" question (no session; only an agent with the secret key).
		mux.HandleFunc("GET /api/update/gate", h.updateGate)
	} else {
		mux.Handle("GET /api/update/status", h.authed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "güncelleme kurulu değil")
		}))
	}

	// Sale: the cashier's own shift must be open.
	mux.Handle("GET /api/sales", h.authed(h.onShift(h.listSales)))
	mux.Handle("GET /api/sales/{id}", h.authed(h.onShift(h.getSale)))
	mux.Handle("POST /api/sales/{id}/checkout", h.authed(h.onShift(h.checkout)))
	mux.Handle("POST /api/sales/{id}/void", h.authed(h.onShift(h.void)))
	mux.Handle("POST /api/sales/{id}/refund", h.authed(h.onShift(h.refund)))
	mux.Handle("/api/", h.authed(h.onShift(toModule(module))))
	// The product images (restaurant menu) are served without a sign-in: <img> cannot
	// carry a session header; the images are not secret and are read-only.
	mux.Handle("GET /api/module/images/", toModule(module))

	// The screen files are served without a sign-in; the sign-in screen is part of them.
	mux.Handle("/", toModule(module))
	return mux
}

type handler struct {
	sales  salesv1.SalesServiceClient
	staff  staffv1.StaffServiceClient
	device devicev1.DeviceServiceClient
	cfg    Config
}

type cashierKey struct{}

// cashierFrom returns the cashier in the request's session; nil if none.
func cashierFrom(r *http.Request) *staffv1.Cashier {
	c, _ := r.Context().Value(cashierKey{}).(*staffv1.Cashier)
	return c
}

func bearerToken(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// authed has staff verify that the request carries a valid cashier session and
// adds the cashier to the context.
func (h *handler) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "giriş yapın")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		resp, err := h.staff.ValidateSession(ctx, &staffv1.ValidateSessionRequest{Token: token})
		cancel()
		if err != nil {
			replyError(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), cashierKey{}, resp.GetCashier())))
	})
}

// onShift opens the request only to a cashier whose own shift is open. There is a single
// drawer at the register; no sale can be made while someone else's shift is open.
func (h *handler) onShift(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		resp, err := h.staff.GetCurrentShift(ctx, &staffv1.GetCurrentShiftRequest{})
		cancel()
		if err != nil {
			replyError(w, err)
			return
		}
		switch sh := resp.GetShift(); {
		case sh == nil:
			writeError(w, http.StatusConflict, "satış için önce vardiya açın")
		case sh.GetCashierId() != cashierFrom(r).GetId():
			writeError(w, http.StatusConflict, "kasada açık vardiya var ("+sh.GetCashierName()+")")
		default:
			next(w, r)
		}
	}
}

// toModule forwards the request to the module. The cashier header sent by the client
// is deleted; if there is a session the gateway puts it itself.
func toModule(module http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(CashierHeader)
		r.Header.Del(CashierNameHeader)
		if c := cashierFrom(r); c != nil {
			r.Header.Set(CashierHeader, c.GetId())
			r.Header.Set(CashierNameHeader, url.QueryEscape(c.GetName()))
		}
		module.ServeHTTP(w, r)
	}
}

type cashierJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func cashierToJSON(c *staffv1.Cashier) cashierJSON {
	role := "cashier"
	if c.GetRole() == staffv1.Role_ROLE_MANAGER {
		role = "manager"
	}
	return cashierJSON{ID: c.GetId(), Name: c.GetName(), Role: role}
}

func (h *handler) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.cfg)
}

func (h *handler) cashiers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	resp, err := h.staff.ListCashiers(ctx, &staffv1.ListCashiersRequest{})
	if err != nil {
		replyError(w, err)
		return
	}
	out := make([]cashierJSON, 0, len(resp.GetCashiers()))
	for _, c := range resp.GetCashiers() {
		out = append(out, cashierToJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CashierID string `json:"cashier_id"`
		PIN       string `json:"pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.Login(ctx, &staffv1.LoginRequest{CashierId: body.CashierID, Pin: body.PIN})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      resp.GetToken(),
		"cashier":    cashierToJSON(resp.GetCashier()),
		"expires_at": resp.GetExpiresAt().AsTime(),
	})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, cashierToJSON(cashierFrom(r)))
}

var actions = map[string]staffv1.Action{
	"close_day":      staffv1.Action_ACTION_CLOSE_DAY,
	"apply_update":   staffv1.Action_ACTION_APPLY_UPDATE,
	"cash_out":       staffv1.Action_ACTION_CASH_OUT,
	"close_shift":    staffv1.Action_ACTION_CLOSE_SHIFT,
	"refund_charged": staffv1.Action_ACTION_REFUND_CHARGED,
	"open_drawer":    staffv1.Action_ACTION_OPEN_DRAWER,
	// The restaurant module checks the approval itself (staff Authorize).
	"void_order_line": staffv1.Action_ACTION_VOID_ORDER_LINE,
	"edit_floor":      staffv1.Action_ACTION_EDIT_FLOOR,
}

// openDrawer opens the drawer without a sale; staff checks the authority (manager or
// manager approval), the opening is recorded in the shift.
func (h *handler) openDrawer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := h.staff.OpenDrawer(ctx, &staffv1.OpenDrawerRequest{
		CashierId: cashierFrom(r).GetId(), ApprovalToken: r.Header.Get(ApprovalHeader), Reason: body.Reason,
	}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// approve gets approval for a single operation for the cashier in the session with the manager's
// PIN; the key returned is carried in the relevant request with ApprovalHeader.
func (h *handler) approve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ManagerID string `json:"manager_id"`
		PIN       string `json:"pin"`
		Action    string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	action, ok := actions[body.Action]
	if !ok {
		writeError(w, http.StatusBadRequest, "bilinmeyen işlem: "+body.Action)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.Approve(ctx, &staffv1.ApproveRequest{
		RequestedBy: cashierFrom(r).GetId(), ManagerId: body.ManagerID, Pin: body.PIN, Action: action,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval_token": resp.GetApprovalToken(),
		"expires_at":     timeOf(resp.GetExpiresAt()),
	})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := h.staff.Logout(ctx, &staffv1.LogoutRequest{Token: bearerToken(r)}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type paymentJSON struct {
	Method      string `json:"method"`
	AmountKurus int64  `json:"amount_kurus"`
	Ref         string `json:"ref,omitempty"`
}

type saleJSON struct {
	ID            string        `json:"id"`
	Status        string        `json:"status"`
	CashierID     string        `json:"cashier_id,omitempty"`
	Refunded      bool          `json:"refunded,omitempty"`
	TotalKurus    int64         `json:"total_kurus"`
	TaxTotalKurus int64         `json:"tax_total_kurus"`
	ChangeKurus   int64         `json:"change_kurus"`
	ReceiptNo     int64         `json:"receipt_no,omitempty"`
	FiscalNo      string        `json:"fiscal_no,omitempty"`
	Payments      []paymentJSON `json:"payments"`
}

var statusNames = map[salesv1.SaleStatus]string{
	salesv1.SaleStatus_SALE_STATUS_OPEN:    "open",
	salesv1.SaleStatus_SALE_STATUS_CHARGED: "charged",
	salesv1.SaleStatus_SALE_STATUS_PAID:    "paid",
	salesv1.SaleStatus_SALE_STATUS_VOIDED:  "voided",
}

// methodName is the payment method's name on the screen; an unrecognized method is
// "other" (the amount does not disappear, "Diğer" on the screen).
func methodName(m salesv1.PaymentMethod) string { return paymethod.NameOrOther(int32(m)) }

func saleToJSON(s *salesv1.Sale) saleJSON {
	out := saleJSON{
		ID:            s.GetId(),
		Status:        statusNames[s.GetStatus()],
		CashierID:     s.GetCashierId(),
		Refunded:      s.GetRefunded(),
		TotalKurus:    s.GetTotalKurus(),
		TaxTotalKurus: s.GetTaxTotalKurus(),
		ChangeKurus:   s.GetChangeKurus(),
		ReceiptNo:     s.GetReceiptNo(),
		FiscalNo:      s.GetFiscalNo(),
		Payments:      []paymentJSON{},
	}
	for _, p := range s.GetPayments() {
		out.Payments = append(out.Payments, paymentJSON{Method: methodName(p.GetMethod()), AmountKurus: p.GetAmountKurus(), Ref: p.GetRef()})
	}
	return out
}

func (h *handler) getSale(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.sales.GetSale(ctx, &salesv1.GetSaleRequest{SaleId: r.PathValue("id")})
	replySale(w, resp.GetSale(), err)
}

var statusCodes = map[string]salesv1.SaleStatus{
	"open":    salesv1.SaleStatus_SALE_STATUS_OPEN,
	"charged": salesv1.SaleStatus_SALE_STATUS_CHARGED,
	"paid":    salesv1.SaleStatus_SALE_STATUS_PAID,
	"voided":  salesv1.SaleStatus_SALE_STATUS_VOIDED,
}

// listSales returns the sales in the given status (?status=charged: sales whose payment
// was taken but whose receipt could not be printed).
func (h *handler) listSales(w http.ResponseWriter, r *http.Request) {
	st, ok := statusCodes[r.URL.Query().Get("status")]
	if !ok {
		writeError(w, http.StatusBadRequest, "status gerekli: open, charged, paid ya da voided")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.sales.ListSales(ctx, &salesv1.ListSalesRequest{Status: st, Limit: 100})
	if err != nil {
		replyError(w, err)
		return
	}
	out := make([]saleJSON, 0, len(resp.GetSales()))
	for _, s := range resp.GetSales() {
		out = append(out, saleToJSON(s))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) checkout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Payments []paymentJSON `json:"payments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	req := &salesv1.CheckoutRequest{SaleId: r.PathValue("id")}
	for _, p := range body.Payments {
		n := paymethod.Number(p.Method)
		if n == 0 {
			writeError(w, http.StatusBadRequest, "bilinmeyen ödeme yöntemi: "+p.Method)
			return
		}
		req.Payments = append(req.Payments, &salesv1.Payment{Method: salesv1.PaymentMethod(n), AmountKurus: p.AmountKurus})
	}
	// A card payment can take long since it waits for the customer to enter their PIN.
	timeout := h.cfg.CheckoutTimeout
	if timeout <= 0 {
		timeout = DefaultCheckoutTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	resp, err := h.sales.Checkout(ctx, req)
	// If the time is up the result is uncertain: the payment may have been taken and the receipt issued.
	// The cashier is told to check the state instead of "try again"; sales writes its own
	// response independently of the cancellation, the sale result is visible later.
	if c := status.Code(err); err != nil && (c == codes.DeadlineExceeded || ctx.Err() != nil) {
		writeError(w, http.StatusServiceUnavailable, "ödeme sonucu alınamadı; ödeme alınmış olabilir. Tekrar ödeme almadan önce satışın durumunu denetleyin (bekleyen satışlar)")
		return
	}
	replySale(w, resp.GetSale(), err)
}

func (h *handler) void(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.sales.VoidSale(ctx, &salesv1.VoidSaleRequest{SaleId: r.PathValue("id")})
	replySale(w, resp.GetSale(), err)
}

// refund gives back the payment of a sale whose receipt could not be printed: first manager
// authority is obtained from staff (a manager session or manager approval with
// ApprovalHeader), then sales refunds the payment and cancels the sale.
func (h *handler) refund(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if _, err := h.staff.Authorize(ctx, &staffv1.AuthorizeRequest{
		CashierId: cashierFrom(r).GetId(), ApprovalToken: r.Header.Get(ApprovalHeader), Action: staffv1.Action_ACTION_REFUND_CHARGED,
	}); err != nil {
		replyError(w, err)
		return
	}
	resp, err := h.sales.RefundCharged(ctx, &salesv1.RefundChargedRequest{SaleId: r.PathValue("id")})
	replySale(w, resp.GetSale(), err)
}

func replySale(w http.ResponseWriter, s *salesv1.Sale, err error) {
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saleToJSON(s))
}

type taxJSON struct {
	RateBp     int64 `json:"rate_bp"`
	GrossKurus int64 `json:"gross_kurus"`
	TaxKurus   int64 `json:"tax_kurus"`
}

type reportJSON struct {
	ZNo              int64         `json:"z_no"`
	OpenedAt         *time.Time    `json:"opened_at,omitempty"`
	ClosedAt         *time.Time    `json:"closed_at,omitempty"`
	ReceiptCount     int64         `json:"receipt_count"`
	FirstReceiptNo   int64         `json:"first_receipt_no"`
	LastReceiptNo    int64         `json:"last_receipt_no"`
	Taxes            []taxJSON     `json:"taxes"`
	TotalKurus       int64         `json:"total_kurus"`
	TaxTotalKurus    int64         `json:"tax_total_kurus"`
	Payments         []paymentJSON `json:"payments"`
	VoidedCount      int64         `json:"voided_count"`
	VoidedTotalKurus int64         `json:"voided_total_kurus"`
}

func (h *handler) xReport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.GetXReport(ctx, &staffv1.GetXReportRequest{})
	replyReport(w, resp.GetReport(), err)
}

func (h *handler) closeDay(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := h.staff.CloseDay(ctx, &staffv1.CloseDayRequest{
		CashierId: cashierFrom(r).GetId(), ApprovalToken: r.Header.Get(ApprovalHeader),
	})
	replyReport(w, resp.GetReport(), err)
}

// printDay prints the X report or the Z report of a closed period (path: zno)
// on the receipt printer.
func (h *handler) printDay(w http.ResponseWriter, r *http.Request) {
	var zNo int64
	if s := r.PathValue("zno"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "geçersiz Z numarası")
			return
		}
		zNo = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := h.staff.PrintDayReport(ctx, &staffv1.PrintDayReportRequest{ZNo: zNo}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// printShift prints the report of a closed shift; staff checks the permission to
// view it.
func (h *handler) printShift(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz vardiya numarası")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := h.staff.PrintShiftReport(ctx, &staffv1.PrintShiftReportRequest{CashierId: cashierFrom(r).GetId(), ShiftId: id}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func replyReport(w http.ResponseWriter, r *staffv1.DayReport, err error) {
	if err != nil {
		replyError(w, err)
		return
	}
	out := reportJSON{
		ZNo:              r.GetZNo(),
		OpenedAt:         timeOf(r.GetOpenedAt()),
		ClosedAt:         timeOf(r.GetClosedAt()),
		ReceiptCount:     r.GetReceiptCount(),
		FirstReceiptNo:   r.GetFirstReceiptNo(),
		LastReceiptNo:    r.GetLastReceiptNo(),
		Taxes:            []taxJSON{},
		TotalKurus:       r.GetTotalKurus(),
		TaxTotalKurus:    r.GetTaxTotalKurus(),
		Payments:         staffPayments(r.GetPayments()),
		VoidedCount:      r.GetVoidedCount(),
		VoidedTotalKurus: r.GetVoidedTotalKurus(),
	}
	for _, t := range r.GetTaxes() {
		out.Taxes = append(out.Taxes, taxJSON{RateBp: t.GetRateBp(), GrossKurus: t.GetGrossKurus(), TaxKurus: t.GetTaxKurus()})
	}
	writeJSON(w, http.StatusOK, out)
}

func staffPayments(payments []*staffv1.PaymentTotal) []paymentJSON {
	out := make([]paymentJSON, 0, len(payments))
	for _, p := range payments {
		// The method numbers are the same in the staff and sales contracts.
		out = append(out, paymentJSON{Method: methodName(salesv1.PaymentMethod(p.GetMethod())), AmountKurus: p.GetAmountKurus()})
	}
	return out
}

// timeOf turns an empty timestamp into nil (the field is not written in JSON).
func timeOf(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

type shiftJSON struct {
	ID               int64      `json:"id"`
	CashierID        string     `json:"cashier_id"`
	CashierName      string     `json:"cashier_name"`
	OpenedAt         *time.Time `json:"opened_at,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	OpeningCashKurus int64      `json:"opening_cash_kurus"`
	CountedCashKurus int64      `json:"counted_cash_kurus"`
	ClosedBy         string     `json:"closed_by,omitempty"`
}

func shiftToJSON(sh *staffv1.Shift) *shiftJSON {
	if sh == nil {
		return nil
	}
	return &shiftJSON{
		ID:               sh.GetId(),
		CashierID:        sh.GetCashierId(),
		CashierName:      sh.GetCashierName(),
		OpenedAt:         timeOf(sh.GetOpenedAt()),
		ClosedAt:         timeOf(sh.GetClosedAt()),
		OpeningCashKurus: sh.GetOpeningCashKurus(),
		CountedCashKurus: sh.GetCountedCashKurus(),
		ClosedBy:         sh.GetClosedBy(),
	}
}

// currentShift returns the open shift at the register; {"shift": null} if none.
func (h *handler) currentShift(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	resp, err := h.staff.GetCurrentShift(ctx, &staffv1.GetCurrentShiftRequest{})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shift": shiftToJSON(resp.GetShift())})
}

// openShift opens the shift of the cashier in the session.
func (h *handler) openShift(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OpeningCashKurus int64 `json:"opening_cash_kurus"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.OpenShift(ctx, &staffv1.OpenShiftRequest{CashierId: cashierFrom(r).GetId(), OpeningCashKurus: body.OpeningCashKurus})
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, shiftToJSON(resp.GetShift()))
}

// closeShift closes the open shift with the counted cash on behalf of the cashier in the session;
// staff checks the authority (the owner or a manager).
func (h *handler) closeShift(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CountedCashKurus int64 `json:"counted_cash_kurus"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := h.staff.CloseShift(ctx, &staffv1.CloseShiftRequest{
		CashierId: cashierFrom(r).GetId(), CountedCashKurus: body.CountedCashKurus, ApprovalToken: r.Header.Get(ApprovalHeader),
	})
	replyShiftReport(w, resp.GetReport(), err)
}

// listShifts returns the closed shifts from newest to oldest (?limit=, at most
// 100); a cashier sees only their own (staff checks).
func (h *handler) listShifts(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.ListShifts(ctx, &staffv1.ListShiftsRequest{CashierId: cashierFrom(r).GetId(), Limit: int32(min(max(limit, 1), 100))})
	if err != nil {
		replyError(w, err)
		return
	}
	out := make([]*shiftJSON, 0, len(resp.GetShifts()))
	for _, sh := range resp.GetShifts() {
		out = append(out, shiftToJSON(sh))
	}
	writeJSON(w, http.StatusOK, out)
}

// shiftReport returns the report of a closed shift.
func (h *handler) shiftReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz vardiya numarası")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.GetShiftReport(ctx, &staffv1.GetShiftReportRequest{CashierId: cashierFrom(r).GetId(), ShiftId: id})
	replyShiftReport(w, resp.GetReport(), err)
}

func replyShiftReport(w http.ResponseWriter, rep *staffv1.ShiftReport, err error) {
	if err != nil {
		replyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shift":               shiftToJSON(rep.GetShift()),
		"receipt_count":       rep.GetReceiptCount(),
		"total_kurus":         rep.GetTotalKurus(),
		"payments":            staffPayments(rep.GetPayments()),
		"voided_count":        rep.GetVoidedCount(),
		"voided_total_kurus":  rep.GetVoidedTotalKurus(),
		"cash_in_kurus":       rep.GetCashInKurus(),
		"cash_out_kurus":      rep.GetCashOutKurus(),
		"drawer_opens":        rep.GetDrawerOpens(),
		"expected_cash_kurus": rep.GetExpectedCashKurus(),
		"difference_kurus":    rep.GetDifferenceKurus(),
	})
}

var movementKinds = map[string]staffv1.CashMovementKind{
	"in":  staffv1.CashMovementKind_CASH_MOVEMENT_KIND_IN,
	"out": staffv1.CashMovementKind_CASH_MOVEMENT_KIND_OUT,
}

// cashMovement records a cash movement (putting money in "in", taking money out "out")
// in the open shift on behalf of the cashier in the session; staff checks the authority.
func (h *handler) cashMovement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind        string `json:"kind"`
		AmountKurus int64  `json:"amount_kurus"`
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSON)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "geçersiz istek")
		return
	}
	kind, ok := movementKinds[body.Kind]
	if !ok {
		writeError(w, http.StatusBadRequest, "bilinmeyen kasa hareketi: "+body.Kind)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.RecordCashMovement(ctx, &staffv1.RecordCashMovementRequest{
		CashierId: cashierFrom(r).GetId(), Kind: kind, AmountKurus: body.AmountKurus, Note: body.Note,
		ApprovalToken: r.Header.Get(ApprovalHeader),
	})
	if err != nil {
		replyError(w, err)
		return
	}
	m := resp.GetMovement()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           m.GetId(),
		"shift_id":     m.GetShiftId(),
		"kind":         body.Kind,
		"amount_kurus": m.GetAmountKurus(),
		"note":         m.GetNote(),
		"cashier_id":   m.GetCashierId(),
		"at":           timeOf(m.GetAt()),
	})
}

// replyError turns a gRPC error into an HTTP response. If the service could not be reached
// at all a plain message goes instead of gRPC's technical message.
func replyError(w http.ResponseWriter, err error) {
	st := status.Convert(service.RemoteErrorFrom("bir kasa servisi", err))
	writeError(w, httpStatus(st.Code()), st.Message())
}

// httpStatus turns gRPC codes into HTTP status codes. The screen interprets 401 as
// "sign in again", 409 as "cannot be done" (payment rejection, shift state), 503 as
// "try again".
func httpStatus(c codes.Code) int {
	switch c {
	case codes.InvalidArgument:
		return http.StatusUnprocessableEntity
	case codes.NotFound:
		return http.StatusNotFound
	case codes.FailedPrecondition:
		return http.StatusConflict
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// updateWrite passes the approval (apply), postponement (snooze) and skipping (skip) of the
// new version to the agent. Staff gives the authority: if the cashier in the session is a manager or
// there is manager approval (ACTION_APPLY_UPDATE); the approving manager's identity goes to the
// agent in the person header and is written to the audit log.
func (h *handler) updateWrite(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("op") {
	case "apply", "skip", "snooze":
	default:
		writeError(w, http.StatusNotFound, "bilinmeyen işlem")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.staff.Authorize(ctx, &staffv1.AuthorizeRequest{
		CashierId: cashierFrom(r).GetId(), ApprovalToken: r.Header.Get(ApprovalHeader), Action: staffv1.Action_ACTION_APPLY_UPDATE,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	r.Header.Del(ApprovalHeader)
	login.Person{Username: resp.GetManagerId(), Name: resp.GetManagerId(), Role: "manager"}.SetHeaders(r.Header)
	r.Header.Set(login.UpdateSecretHeader, h.cfg.UpdateSecret)
	h.cfg.Update.ServeHTTP(w, r)
}

// updateGate says whether the update is at a safe moment: sales may be going on
// while a shift is open at the register, so it is not applied.
func (h *handler) updateGate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UpdateSecret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(login.UpdateSecretHeader)), []byte(h.cfg.UpdateSecret)) != 1 {
		writeError(w, http.StatusForbidden, "yetkisiz")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	resp, err := h.staff.GetCurrentShift(ctx, &staffv1.GetCurrentShiftRequest{})
	if err != nil {
		replyError(w, err)
		return
	}
	if sh := resp.GetShift(); sh != nil {
		writeJSON(w, http.StatusOK, map[string]any{"safe": false, "reason": "kasada açık vardiya var (" + sh.GetCashierName() + ")"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"safe": true})
}
