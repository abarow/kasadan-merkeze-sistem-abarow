// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/login"
	"abarow.com/services/core/gateway/internal/httpapi"
)

// fakeSales is a fake sales client that implements only Checkout.
type fakeSales struct {
	salesv1.SalesServiceClient
	got      *salesv1.CheckoutRequest
	err      error
	refunded string
}

func (f *fakeSales) ListSales(_ context.Context, req *salesv1.ListSalesRequest, _ ...grpc.CallOption) (*salesv1.ListSalesResponse, error) {
	if req.GetStatus() != salesv1.SaleStatus_SALE_STATUS_CHARGED {
		return &salesv1.ListSalesResponse{}, nil
	}
	return &salesv1.ListSalesResponse{Sales: []*salesv1.Sale{{Id: "s9", Status: salesv1.SaleStatus_SALE_STATUS_CHARGED, TotalKurus: 4900}}}, nil
}

func (f *fakeSales) RefundCharged(_ context.Context, req *salesv1.RefundChargedRequest, _ ...grpc.CallOption) (*salesv1.RefundChargedResponse, error) {
	f.refunded = req.GetSaleId()
	return &salesv1.RefundChargedResponse{Sale: &salesv1.Sale{Id: req.GetSaleId(), Status: salesv1.SaleStatus_SALE_STATUS_VOIDED, Refunded: true}}, nil
}

func (f *fakeSales) Checkout(_ context.Context, req *salesv1.CheckoutRequest, _ ...grpc.CallOption) (*salesv1.CheckoutResponse, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return &salesv1.CheckoutResponse{Sale: &salesv1.Sale{
		Id: req.GetSaleId(), Status: salesv1.SaleStatus_SALE_STATUS_PAID, TotalKurus: 12537, TaxTotalKurus: 328,
		ChangeKurus: 7463, ReceiptNo: 4, FiscalNo: "SIM-000004",
		Payments: []*salesv1.Payment{{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: 20000}},
	}}, nil
}

// fakeStaff: the keys "cashier" and "manager" are valid sessions; the open shift
// at the register is shift (nil if none).
type fakeStaff struct {
	staffv1.StaffServiceClient
	shift    *staffv1.Shift
	closed   *staffv1.CloseShiftRequest
	movement *staffv1.RecordCashMovementRequest
	listed   *staffv1.ListShiftsRequest
	approved *staffv1.ApproveRequest

	printedDay   []int64
	printedShift *staffv1.PrintShiftReportRequest
	drawer       *staffv1.OpenDrawerRequest
	// down makes staff unreachable (a restart).
	down bool
}

var errStaffDown = status.Error(codes.Unavailable, "connection refused")

// ayseShift is Cashier Ayşe's open shift.
var ayseShift = &staffv1.Shift{Id: 1, CashierId: "ayse", CashierName: "Kasiyer Ayşe", OpenedAt: timestamppb.Now(), OpeningCashKurus: 50000}

var sessions = map[string]*staffv1.Cashier{
	"kasiyer": {Id: "ayse", Name: "Kasiyer Ayşe", Role: staffv1.Role_ROLE_CASHIER},
	"mudur":   {Id: "ali", Name: "Müdür Ali", Role: staffv1.Role_ROLE_MANAGER},
}

func (f *fakeStaff) ValidateSession(_ context.Context, req *staffv1.ValidateSessionRequest, _ ...grpc.CallOption) (*staffv1.ValidateSessionResponse, error) {
	if f.down {
		return nil, errStaffDown
	}
	c, ok := sessions[req.GetToken()]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "oturum geçersiz")
	}
	return &staffv1.ValidateSessionResponse{Cashier: c}, nil
}

func (*fakeStaff) GetXReport(context.Context, *staffv1.GetXReportRequest, ...grpc.CallOption) (*staffv1.GetXReportResponse, error) {
	return &staffv1.GetXReportResponse{Report: &staffv1.DayReport{
		ZNo: 3, OpenedAt: timestamppb.Now(), ReceiptCount: 2, FirstReceiptNo: 11, LastReceiptNo: 12, TotalKurus: 17437, TaxTotalKurus: 790,
		Taxes:    []*staffv1.TaxTotal{{RateBp: 100, GrossKurus: 12438, TaxKurus: 123}},
		Payments: []*staffv1.PaymentTotal{{Method: staffv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: 15437}, {Method: staffv1.PaymentMethod_PAYMENT_METHOD_CARD, AmountKurus: 2000}},
	}}, nil
}

// CloseDay: a manager, or a cashier with the "approval" key, can close the day.
func (*fakeStaff) CloseDay(_ context.Context, req *staffv1.CloseDayRequest, _ ...grpc.CallOption) (*staffv1.CloseDayResponse, error) {
	if req.GetCashierId() != "ali" && req.GetApprovalToken() != "onay" {
		return nil, status.Error(codes.PermissionDenied, "gün sonu (Z) için müdür yetkisi gerekli")
	}
	return &staffv1.CloseDayResponse{Report: &staffv1.DayReport{ZNo: 3, ClosedAt: timestamppb.Now()}}, nil
}

// Approve: Manager Ali's PIN is 9999; a wrong PIN returns 403 (it does not drop the session).
func (f *fakeStaff) Approve(_ context.Context, req *staffv1.ApproveRequest, _ ...grpc.CallOption) (*staffv1.ApproveResponse, error) {
	f.approved = req
	if req.GetManagerId() != "ali" || req.GetPin() != "9999" {
		return nil, status.Error(codes.PermissionDenied, "PIN hatalı; 4 deneme hakkı kaldı")
	}
	return &staffv1.ApproveResponse{ApprovalToken: "onay", ExpiresAt: timestamppb.Now()}, nil
}

func (f *fakeStaff) Logout(context.Context, *staffv1.LogoutRequest, ...grpc.CallOption) (*staffv1.LogoutResponse, error) {
	if f.down {
		return nil, errStaffDown
	}
	return &staffv1.LogoutResponse{}, nil
}

func (f *fakeStaff) GetCurrentShift(context.Context, *staffv1.GetCurrentShiftRequest, ...grpc.CallOption) (*staffv1.GetCurrentShiftResponse, error) {
	if f.down {
		return nil, errStaffDown
	}
	return &staffv1.GetCurrentShiftResponse{Shift: f.shift}, nil
}

func (f *fakeStaff) OpenShift(_ context.Context, req *staffv1.OpenShiftRequest, _ ...grpc.CallOption) (*staffv1.OpenShiftResponse, error) {
	if f.shift != nil {
		return nil, status.Error(codes.FailedPrecondition, "kasada açık vardiya var ("+f.shift.GetCashierName()+")")
	}
	f.shift = &staffv1.Shift{Id: 2, CashierId: req.GetCashierId(), OpeningCashKurus: req.GetOpeningCashKurus()}
	return &staffv1.OpenShiftResponse{Shift: f.shift}, nil
}

func (f *fakeStaff) CloseShift(_ context.Context, req *staffv1.CloseShiftRequest, _ ...grpc.CallOption) (*staffv1.CloseShiftResponse, error) {
	f.closed = req
	return &staffv1.CloseShiftResponse{Report: &staffv1.ShiftReport{
		Shift: f.shift, ReceiptCount: 2, TotalKurus: 3500,
		Payments:          []*staffv1.PaymentTotal{{Method: staffv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: 3500}},
		ExpectedCashKurus: 53500, DifferenceKurus: req.GetCountedCashKurus() - 53500,
	}}, nil
}

func (f *fakeStaff) RecordCashMovement(_ context.Context, req *staffv1.RecordCashMovementRequest, _ ...grpc.CallOption) (*staffv1.RecordCashMovementResponse, error) {
	f.movement = req
	return &staffv1.RecordCashMovementResponse{Movement: &staffv1.CashMovement{
		Id: 1, ShiftId: 1, Kind: req.GetKind(), AmountKurus: req.GetAmountKurus(), Note: req.GetNote(), CashierId: req.GetCashierId(),
	}}, nil
}

func (f *fakeStaff) ListShifts(_ context.Context, req *staffv1.ListShiftsRequest, _ ...grpc.CallOption) (*staffv1.ListShiftsResponse, error) {
	f.listed = req
	return &staffv1.ListShiftsResponse{Shifts: []*staffv1.Shift{{Id: 7, CashierId: "ayse", CashierName: "Kasiyer Ayşe", ClosedAt: timestamppb.Now()}}}, nil
}

func (f *fakeStaff) GetShiftReport(_ context.Context, req *staffv1.GetShiftReportRequest, _ ...grpc.CallOption) (*staffv1.GetShiftReportResponse, error) {
	if req.GetCashierId() != "ali" {
		return nil, status.Error(codes.PermissionDenied, "bu işlemi yalnızca vardiyanın sahibi ya da müdür yapabilir")
	}
	return &staffv1.GetShiftReportResponse{Report: &staffv1.ShiftReport{
		Shift: &staffv1.Shift{Id: req.GetShiftId()}, ExpectedCashKurus: 53500, CashInKurus: 10000,
	}}, nil
}

func (f *fakeStaff) PrintDayReport(_ context.Context, req *staffv1.PrintDayReportRequest, _ ...grpc.CallOption) (*staffv1.PrintDayReportResponse, error) {
	f.printedDay = append(f.printedDay, req.GetZNo())
	return &staffv1.PrintDayReportResponse{}, nil
}

func (f *fakeStaff) PrintShiftReport(_ context.Context, req *staffv1.PrintShiftReportRequest, _ ...grpc.CallOption) (*staffv1.PrintShiftReportResponse, error) {
	f.printedShift = req
	return &staffv1.PrintShiftReportResponse{}, nil
}

func (f *fakeStaff) OpenDrawer(_ context.Context, req *staffv1.OpenDrawerRequest, _ ...grpc.CallOption) (*staffv1.OpenDrawerResponse, error) {
	f.drawer = req
	return &staffv1.OpenDrawerResponse{}, nil
}

// Authorize: a manager, or a cashier with the "approval" key.
func (f *fakeStaff) Authorize(_ context.Context, req *staffv1.AuthorizeRequest, _ ...grpc.CallOption) (*staffv1.AuthorizeResponse, error) {
	if req.GetCashierId() != "ali" && req.GetApprovalToken() != "onay" {
		return nil, status.Error(codes.PermissionDenied, "bu işlem için müdür yetkisi gerekli")
	}
	return &staffv1.AuthorizeResponse{ManagerId: "ali"}, nil
}

// server starts the gateway with a fake module that writes the path of the request that
// comes instead of the module and the cashier header. Ayşe's shift is open at the register.
func server(sales *fakeSales) *httptest.Server {
	return serverWith(sales, &fakeStaff{shift: ayseShift})
}

func serverWith(sales *fakeSales, staff *fakeStaff) *httptest.Server {
	module := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "modül:"+r.URL.Path+" kasiyer:"+r.Header.Get(httpapi.CashierHeader))
	})
	cfg := httpapi.Config{Module: "retail", FiscalMode: "simulator", StoreID: "store-1", RegisterID: "register-1"}
	return httptest.NewServer(httpapi.New(sales, staff, nil, module, cfg))
}

// do sends a request with the given session key; no session if token is empty.
func do(t *testing.T, method, url, token, body string, header ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestCheckoutTranslatesToSales(t *testing.T) {
	sales := &fakeSales{}
	srv := server(sales)
	defer srv.Close()

	code, body := do(t, http.MethodPost, srv.URL+"/api/sales/s1/checkout", "kasiyer", `{"payments":[{"method":"cash","amount_kurus":20000}]}`)
	if code != http.StatusOK {
		t.Fatalf("durum %d: %s", code, body)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	if out["status"] != "paid" || out["receipt_no"] != float64(4) || out["change_kurus"] != float64(7463) || out["fiscal_no"] != "SIM-000004" {
		t.Errorf("response is wrong: %v", out)
	}
	p := sales.got.GetPayments()
	if sales.got.GetSaleId() != "s1" || len(p) != 1 || p[0].GetMethod() != salesv1.PaymentMethod_PAYMENT_METHOD_CASH || p[0].GetAmountKurus() != 20000 {
		t.Errorf("the request that went to sales is wrong: %v", sales.got)
	}
}

func TestCheckoutErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		body string
		want int
	}{
		{"kart reddi", status.Error(codes.FailedPrecondition, "kart ödemesi reddedildi"), `{"payments":[{"method":"card","amount_kurus":1}]}`, http.StatusConflict},
		{"ÖKC yanıt vermiyor", status.Error(codes.Unavailable, "mali kayıt yapılamadı"), `{"payments":[{"method":"card","amount_kurus":1}]}`, http.StatusServiceUnavailable},
		{"eksik ödeme", status.Error(codes.InvalidArgument, "ödeme toplamı satış tutarından az"), `{"payments":[{"method":"cash","amount_kurus":1}]}`, http.StatusUnprocessableEntity},
		{"bilinmeyen yöntem", nil, `{"payments":[{"method":"cek","amount_kurus":1}]}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := server(&fakeSales{err: tt.err})
			defer srv.Close()
			code, body := do(t, http.MethodPost, srv.URL+"/api/sales/s1/checkout", "kasiyer", tt.body)
			if code != tt.want || !strings.Contains(body, `"error"`) {
				t.Errorf("durum %d (%s), istenen %d", code, body, tt.want)
			}
		})
	}
}

func TestAuthorization(t *testing.T) {
	srv := server(&fakeSales{})
	defer srv.Close()
	tests := []struct {
		name, method, path, token string
		want                      int
	}{
		{"kurulum bilgisi girişsiz", http.MethodGet, "/api/config", "", http.StatusOK},
		{"ekran dosyaları girişsiz", http.MethodGet, "/", "", http.StatusOK},
		{"ödeme girişsiz olmaz", http.MethodPost, "/api/sales/s1/checkout", "", http.StatusUnauthorized},
		{"modül API'si girişsiz olmaz", http.MethodPost, "/api/module/baskets", "", http.StatusUnauthorized},
		{"ürün görseli girişsiz", http.MethodGet, "/api/module/images/kofte", "", http.StatusOK},
		{"görsel yolundan yazma girişsiz olmaz", http.MethodPost, "/api/module/images/kofte", "", http.StatusUnauthorized},
		{"uydurma oturum", http.MethodGet, "/api/reports/x", "uydurma", http.StatusUnauthorized},
		{"kasiyer X alabilir", http.MethodGet, "/api/reports/x", "kasiyer", http.StatusOK},
		{"kasiyer Z alamaz", http.MethodPost, "/api/reports/z", "kasiyer", http.StatusForbidden},
		{"müdür Z alabilir", http.MethodPost, "/api/reports/z", "mudur", http.StatusOK},
		{"vardiya bilgisi", http.MethodGet, "/api/shift", "kasiyer", http.StatusOK},
		// Ayşe's shift is open at the register: a manager cannot make a sale, can take an X.
		{"başkasının vardiyasında satış olmaz", http.MethodPost, "/api/module/baskets", "mudur", http.StatusConflict},
		{"başkasının vardiyasında ödeme olmaz", http.MethodPost, "/api/sales/s1/checkout", "mudur", http.StatusConflict},
		{"müdür X alabilir", http.MethodGet, "/api/reports/x", "mudur", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, body := do(t, tt.method, srv.URL+tt.path, tt.token, ""); code != tt.want {
				t.Errorf("durum %d (%s), istenen %d", code, body, tt.want)
			}
		})
	}
}

func TestModuleGetsCashierFromSessionOnly(t *testing.T) {
	srv := server(&fakeSales{})
	defer srv.Close()

	// The cashier header sent by the client is ignored; the cashier in the session goes.
	code, body := do(t, http.MethodPost, srv.URL+"/api/module/baskets", "kasiyer", "", httpapi.CashierHeader, "sahte")
	if code != http.StatusOK || body != "modül:/api/module/baskets kasiyer:ayse" {
		t.Errorf("request that went to the module: %d %q", code, body)
	}
	// There is no session in the screen files; a fake header is still deleted.
	if _, body := do(t, http.MethodGet, srv.URL+"/index.html", "", "", httpapi.CashierHeader, "sahte"); body != "modül:/index.html kasiyer:" {
		t.Errorf("screen file request: %q", body)
	}
}

func TestXReport(t *testing.T) {
	srv := server(&fakeSales{})
	defer srv.Close()
	code, body := do(t, http.MethodGet, srv.URL+"/api/reports/x", "kasiyer", "")
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != http.StatusOK {
		t.Fatalf("durum %d: %s", code, body)
	}
	payments, _ := out["payments"].([]any)
	if out["z_no"] != float64(3) || out["total_kurus"] != float64(17437) || out["opened_at"] == nil || out["closed_at"] != nil || len(payments) != 2 {
		t.Errorf("X report is wrong: %v", out)
	}
}

func TestShift(t *testing.T) {
	staff := &fakeStaff{}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()

	// No sale can be made without a shift; reports can be taken.
	if code, body := do(t, http.MethodPost, srv.URL+"/api/module/baskets", "kasiyer", ""); code != http.StatusConflict || !strings.Contains(body, "vardiya açın") {
		t.Errorf("sale without a shift: %d %s", code, body)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/reports/x", "kasiyer", ""); code != http.StatusOK {
		t.Errorf("X without a shift: %d", code)
	}
	if _, body := do(t, http.MethodGet, srv.URL+"/api/shift", "kasiyer", ""); body != `{"shift":null}`+"\n" {
		t.Errorf("empty shift: %q", body)
	}

	// The shift is opened on behalf of the cashier in the session; the id in the body is ignored.
	code, body := do(t, http.MethodPost, srv.URL+"/api/shift/open", "kasiyer", `{"cashier_id":"ali","opening_cash_kurus":50000}`)
	if code != http.StatusOK || staff.shift.GetCashierId() != "ayse" || staff.shift.GetOpeningCashKurus() != 50000 {
		t.Fatalf("shift could not be opened: %d %s", code, body)
	}
	if code, body := do(t, http.MethodPost, srv.URL+"/api/module/baskets", "kasiyer", ""); code != http.StatusOK {
		t.Errorf("sale in the shift: %d %s", code, body)
	}
	if code, body := do(t, http.MethodPost, srv.URL+"/api/shift/open", "mudur", `{"opening_cash_kurus":0}`); code != http.StatusConflict || !strings.Contains(body, "açık vardiya") {
		t.Errorf("ikinci vardiya: %d %s", code, body)
	}

	// Closing is on behalf of the cashier in the session; the report carries the expected cash and the difference.
	code, body = do(t, http.MethodPost, srv.URL+"/api/shift/close", "mudur", `{"counted_cash_kurus":53400}`)
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != http.StatusOK {
		t.Fatalf("closing: %d %s", code, body)
	}
	if staff.closed.GetCashierId() != "ali" || staff.closed.GetCountedCashKurus() != 53400 {
		t.Errorf("closing that went to staff: %v", staff.closed)
	}
	if out["expected_cash_kurus"] != float64(53500) || out["difference_kurus"] != float64(-100) || out["receipt_count"] != float64(2) {
		t.Errorf("vardiya raporu: %v", out)
	}
}

func TestCashMovement(t *testing.T) {
	staff := &fakeStaff{shift: ayseShift}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()

	// The cashier id comes from the session; the direction is translated for staff.
	code, body := do(t, http.MethodPost, srv.URL+"/api/shift/cash", "mudur", `{"kind":"out","amount_kurus":20000,"note":"Ara para","cashier_id":"ayse"}`)
	if code != http.StatusOK || !strings.Contains(body, `"kind":"out"`) {
		t.Fatalf("hareket: %d %s", code, body)
	}
	m := staff.movement
	if m.GetCashierId() != "ali" || m.GetKind() != staffv1.CashMovementKind_CASH_MOVEMENT_KIND_OUT || m.GetAmountKurus() != 20000 || m.GetNote() != "Ara para" {
		t.Errorf("staff'a giden hareket: %v", m)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/shift/cash", "mudur", `{"kind":"avans","amount_kurus":1,"note":"?"}`); code != http.StatusBadRequest {
		t.Errorf("unknown direction: %d", code)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/shift/cash", "", `{"kind":"in","amount_kurus":1,"note":"?"}`); code != http.StatusUnauthorized {
		t.Errorf("movement without a sign-in: %d", code)
	}
}

func TestShiftHistory(t *testing.T) {
	staff := &fakeStaff{}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()

	// The list is requested on behalf of the cashier in the session; the limit is clamped to 1-100.
	code, body := do(t, http.MethodGet, srv.URL+"/api/shifts?limit=500", "kasiyer", "")
	if code != http.StatusOK || !strings.Contains(body, `"cashier_name":"Kasiyer Ayşe"`) {
		t.Fatalf("liste: %d %s", code, body)
	}
	if staff.listed.GetCashierId() != "ayse" || staff.listed.GetLimit() != 100 {
		t.Errorf("list request that went to staff: %v", staff.listed)
	}

	code, body = do(t, http.MethodGet, srv.URL+"/api/shifts/7/report", "mudur", "")
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != http.StatusOK {
		t.Fatalf("rapor: %d %s", code, body)
	}
	if out["expected_cash_kurus"] != float64(53500) || out["cash_in_kurus"] != float64(10000) {
		t.Errorf("rapor: %v", out)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/shifts/7/report", "kasiyer", ""); code != http.StatusForbidden {
		t.Errorf("someone else's report: %d", code)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/shifts/abc/report", "mudur", ""); code != http.StatusBadRequest {
		t.Errorf("invalid number: %d", code)
	}
}

func TestManagerApproval(t *testing.T) {
	staff := &fakeStaff{shift: ayseShift}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()

	// A wrong PIN is 403; if it were 401 the screen would close the cashier's session.
	if code, body := do(t, http.MethodPost, srv.URL+"/api/auth/approve", "kasiyer", `{"manager_id":"ali","pin":"0000","action":"close_day"}`); code != http.StatusForbidden {
		t.Errorf("wrong approval PIN: %d %s", code, body)
	}
	code, body := do(t, http.MethodPost, srv.URL+"/api/auth/approve", "kasiyer", `{"manager_id":"ali","pin":"9999","action":"close_day"}`)
	if code != http.StatusOK || !strings.Contains(body, `"approval_token":"onay"`) {
		t.Fatalf("onay: %d %s", code, body)
	}
	if staff.approved.GetRequestedBy() != "ayse" || staff.approved.GetAction() != staffv1.Action_ACTION_CLOSE_DAY {
		t.Errorf("approval request that went to staff: %v", staff.approved)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/auth/approve", "kasiyer", `{"manager_id":"ali","pin":"9999","action":"her_sey"}`); code != http.StatusBadRequest {
		t.Errorf("unknown operation: %d", code)
	}
	// The approval key is passed to staff in a header.
	if code, body := do(t, http.MethodPost, srv.URL+"/api/reports/z", "kasiyer", "", httpapi.ApprovalHeader, "onay"); code != http.StatusOK {
		t.Errorf("approved Z: %d %s", code, body)
	}
}

func TestPrintReports(t *testing.T) {
	staff := &fakeStaff{}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()

	for _, path := range []string{"/api/reports/x/print", "/api/reports/z/5/print"} {
		if code, body := do(t, http.MethodPost, srv.URL+path, "kasiyer", ""); code != http.StatusNoContent {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	if len(staff.printedDay) != 2 || staff.printedDay[0] != 0 || staff.printedDay[1] != 5 {
		t.Errorf("X/Z printed: %v; want [0 5]", staff.printedDay)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/reports/z/0/print", "kasiyer", ""); code != http.StatusBadRequest {
		t.Errorf("Z no 0: %d", code)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/shifts/7/print", "kasiyer", ""); code != http.StatusNoContent {
		t.Errorf("shift print: %d", code)
	}
	if staff.printedShift.GetCashierId() != "ayse" || staff.printedShift.GetShiftId() != 7 {
		t.Errorf("print request that went to staff: %v", staff.printedShift)
	}
}

func TestListChargedSales(t *testing.T) {
	srv := server(&fakeSales{})
	defer srv.Close()
	code, body := do(t, http.MethodGet, srv.URL+"/api/sales?status=charged", "kasiyer", "")
	if code != http.StatusOK || !strings.Contains(body, `"id":"s9"`) || !strings.Contains(body, `"status":"charged"`) {
		t.Errorf("charged sales: %d %s", code, body)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/sales?status=hepsi", "kasiyer", ""); code != http.StatusBadRequest {
		t.Errorf("bilinmeyen durum: %d", code)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/sales?status=charged", "mudur", ""); code != http.StatusConflict {
		t.Errorf("cashier with no shift: %d", code)
	}
}

func TestRefundNeedsManager(t *testing.T) {
	sales := &fakeSales{}
	srv := server(sales)
	defer srv.Close()

	// A cashier cannot refund without approval; sales is never reached.
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/sales/s9/refund", "kasiyer", ""); code != http.StatusForbidden || sales.refunded != "" {
		t.Errorf("refund without approval: %d, went to sales %q", code, sales.refunded)
	}
	code, body := do(t, http.MethodPost, srv.URL+"/api/sales/s9/refund", "kasiyer", "", httpapi.ApprovalHeader, "onay")
	if code != http.StatusOK || sales.refunded != "s9" || !strings.Contains(body, `"refunded":true`) {
		t.Errorf("approved refund: %d %s", code, body)
	}
}

func TestOpenDrawer(t *testing.T) {
	staff := &fakeStaff{shift: ayseShift}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()
	code, _ := do(t, http.MethodPost, srv.URL+"/api/shift/drawer", "kasiyer", `{"reason":"Bozuk para"}`, httpapi.ApprovalHeader, "onay")
	if code != http.StatusNoContent {
		t.Fatalf("drawer: %d", code)
	}
	if d := staff.drawer; d.GetCashierId() != "ayse" || d.GetApprovalToken() != "onay" || d.GetReason() != "Bozuk para" {
		t.Errorf("staff'a giden istek: %v", d)
	}
}

func TestUnreachableServiceMessage(t *testing.T) {
	srv := server(&fakeSales{err: status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial tcp 10.89.0.69:9000: i/o timeout"`)})
	defer srv.Close()
	code, body := do(t, http.MethodPost, srv.URL+"/api/sales/s1/checkout", "kasiyer", `{"payments":[{"method":"cash","amount_kurus":1}]}`)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "bir kasa servisi yanıt vermiyor") || strings.Contains(body, "dial tcp") {
		t.Errorf("unreachable service: %d %s", code, body)
	}
}

// Gateway shows staff's payment totals with sales' method names:
// the method numbers and names must be the same in the two contracts.
func TestPaymentMethodNumbersMatch(t *testing.T) {
	for n, name := range staffv1.PaymentMethod_name {
		if salesv1.PaymentMethod_name[n] != name {
			t.Errorf("%d: staff %s, sales %s", n, name, salesv1.PaymentMethod_name[n])
		}
	}
	if len(staffv1.PaymentMethod_name) != len(salesv1.PaymentMethod_name) {
		t.Error("the method numbers differ")
	}
}

// The update status asks for a cashier session; 404 if not installed, if installed the agent's response comes.
func TestUpdateStatus(t *testing.T) {
	staff := &fakeStaff{shift: ayseShift}
	module := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "modül") })
	agent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":"pending","code":"ABCD-2345","path":"`+r.URL.Path+`"}`)
	})
	cfg := httpapi.Config{Module: "retail", StoreID: "store-1", RegisterID: "register-1"}

	installed := httptest.NewServer(httpapi.New(&fakeSales{}, staff, nil, module, func() httpapi.Config { c := cfg; c.Update = agent; return c }()))
	defer installed.Close()
	degil := httptest.NewServer(httpapi.New(&fakeSales{}, staff, nil, module, cfg))
	defer degil.Close()

	if code, _ := do(t, http.MethodGet, installed.URL+"/api/update/status", "", ""); code != http.StatusUnauthorized {
		t.Errorf("oturumsuz durum %d, istenen 401", code)
	}
	code, body := do(t, http.MethodGet, installed.URL+"/api/update/status", "kasiyer", "")
	if code != http.StatusOK || !strings.Contains(body, `"code":"ABCD-2345"`) || !strings.Contains(body, `"path":"/api/update/status"`) {
		t.Errorf("kurulu: %d %s", code, body)
	}
	// A manager with no shift (someone else's shift is open) sees it too: the approval code is not tied to the shift.
	if code, _ := do(t, http.MethodGet, installed.URL+"/api/update/status", "mudur", ""); code != http.StatusOK {
		t.Errorf("the manager should see the status: %d", code)
	}
	if code, body := do(t, http.MethodGet, degil.URL+"/api/update/status", "kasiyer", ""); code != http.StatusNotFound || !strings.Contains(body, "kurulu değil") {
		t.Errorf("not installed: %d %s", code, body)
	}
}

func TestUpdateWriteNeedsManagerApproval(t *testing.T) {
	var got struct {
		path, user, role, secret, approval string
		called                             int
	}
	agent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.called++
		got.path, got.user, got.role = r.URL.Path, r.Header.Get(login.UserHeader), r.Header.Get(login.RoleHeader)
		got.secret, got.approval = r.Header.Get(login.UpdateSecretHeader), r.Header.Get(httpapi.ApprovalHeader)
		io.WriteString(w, `{"phase":"applying"}`)
	})
	module := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	cfg := httpapi.Config{Module: "retail", Update: agent, UpdateSecret: "gizli"}
	srv := httptest.NewServer(httpapi.New(&fakeSales{}, &fakeStaff{shift: ayseShift}, nil, module, cfg))
	defer srv.Close()

	// No session: 401. A cashier without approval: 403, it does not reach the agent.
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/update/apply", "", `{"seq":3}`, "Content-Type", "application/json"); code != http.StatusUnauthorized {
		t.Errorf("oturumsuz %d", code)
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/update/apply", "kasiyer", `{"seq":3}`, "Content-Type", "application/json"); code != http.StatusForbidden || got.called != 0 {
		t.Errorf("cashier without approval: %d, reached the agent: %d", code, got.called)
	}
	// Manager approval (cashier + approval key): it is forwarded; the approving manager goes in the person header, with the secret key added.
	code, body := do(t, http.MethodPost, srv.URL+"/api/update/apply", "kasiyer", `{"seq":3}`,
		"Content-Type", "application/json", httpapi.ApprovalHeader, "onay", login.UserHeader, "sahte", login.UpdateSecretHeader, "sahte")
	if code != http.StatusOK || !strings.Contains(body, "applying") {
		t.Fatalf("manager approval: %d %s", code, body)
	}
	if got.path != "/api/update/apply" || got.user != "ali" || got.role != "manager" || got.secret != "gizli" || got.approval != "" {
		t.Errorf("ajana giden: %+v", got)
	}
	// A manager session can do it without approval too; skip and snooze follow the same rule.
	for _, op := range []string{"skip", "snooze"} {
		if code, _ := do(t, http.MethodPost, srv.URL+"/api/update/"+op, "mudur", `{}`, "Content-Type", "application/json"); code != http.StatusOK || got.path != "/api/update/"+op {
			t.Errorf("%s: %d %+v", op, code, got)
		}
	}
	if code, _ := do(t, http.MethodPost, srv.URL+"/api/update/yok", "mudur", `{}`, "Content-Type", "application/json"); code != http.StatusNotFound {
		t.Errorf("unknown operation %d", code)
	}
}

func TestUpdateGate(t *testing.T) {
	agent := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	module := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cfg := httpapi.Config{Module: "retail", Update: agent, UpdateSecret: "gizli"}
	open := httptest.NewServer(httpapi.New(&fakeSales{}, &fakeStaff{shift: ayseShift}, nil, module, cfg))
	defer open.Close()
	closed := httptest.NewServer(httpapi.New(&fakeSales{}, &fakeStaff{}, nil, module, cfg))
	defer closed.Close()

	if code, _ := do(t, http.MethodGet, open.URL+"/api/update/gate", "", ""); code != http.StatusForbidden {
		t.Errorf("without the secret key: %d", code)
	}
	code, body := do(t, http.MethodGet, open.URL+"/api/update/gate", "", "", login.UpdateSecretHeader, "gizli")
	if code != http.StatusOK || !strings.Contains(body, `"safe":false`) || !strings.Contains(body, "Kasiyer Ayşe") {
		t.Errorf("open shift: %d %s", code, body)
	}
	code, body = do(t, http.MethodGet, closed.URL+"/api/update/gate", "", "", login.UpdateSecretHeader, "gizli")
	if code != http.StatusOK || !strings.Contains(body, `"safe":true`) {
		t.Errorf("shift closed: %d %s", code, body)
	}
}

// hangingSales: Checkout returns only when the request context ends (a frozen fiscal device).
type hangingSales struct{ fakeSales }

func (hangingSales) Checkout(ctx context.Context, _ *salesv1.CheckoutRequest, _ ...grpc.CallOption) (*salesv1.CheckoutResponse, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// If sales freezes the payment request does not hang forever: when the time is up the cashier
// is told the payment may have been taken.
func TestCheckoutTimesOutWhenSalesHangs(t *testing.T) {
	module := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cfg := httpapi.Config{Module: "retail", CheckoutTimeout: 150 * time.Millisecond}
	srv := httptest.NewServer(httpapi.New(&hangingSales{}, &fakeStaff{shift: ayseShift}, nil, module, cfg))
	defer srv.Close()

	start := time.Now()
	code, body := do(t, http.MethodPost, srv.URL+"/api/sales/s1/checkout", "kasiyer", `{"payments":[{"method":"cash","amount_kurus":20000}]}`, "Content-Type", "application/json")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "ödeme alınmış olabilir") {
		t.Fatalf("%d %s", code, body)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("timeout took %v", d)
	}
}

func TestDefaultCheckoutTimeoutCoversPaymentAndFiscal(t *testing.T) {
	// payment 90 s + fiscal 30 s + a write margin.
	if httpapi.DefaultCheckoutTimeout < 90*time.Second+30*time.Second+30*time.Second {
		t.Fatalf("the gate time %v is shorter than the total of the sub-calls", httpapi.DefaultCheckoutTimeout)
	}
}

// While staff restarts, a cashier who was just working goes on with the last answers
// of staff (session, open shift); a session staff never confirmed and a signed-out one
// do not pass.
func TestStaffOutage(t *testing.T) {
	staff := &fakeStaff{shift: ayseShift}
	srv := serverWith(&fakeSales{}, staff)
	defer srv.Close()
	if code, body := do(t, http.MethodGet, srv.URL+"/api/basket", "kasiyer", ""); code != http.StatusOK {
		t.Fatalf("staff up: %d %s", code, body)
	}
	staff.down = true
	if code, body := do(t, http.MethodGet, srv.URL+"/api/basket", "kasiyer", ""); code != http.StatusOK || !strings.Contains(body, "kasiyer:ayse") {
		t.Errorf("staff down, known session: %d %s", code, body)
	}
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/basket", "mudur", ""); code != http.StatusServiceUnavailable {
		t.Errorf("staff down, a session never confirmed: %d", code)
	}
	// Signing out ends the session at the gateway even though staff cannot be reached.
	do(t, http.MethodPost, srv.URL+"/api/auth/logout", "kasiyer", "")
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/basket", "kasiyer", ""); code != http.StatusServiceUnavailable {
		t.Errorf("signed out during the outage: %d", code)
	}
	staff.down = false
	if code, _ := do(t, http.MethodGet, srv.URL+"/api/basket", "yok", ""); code != http.StatusUnauthorized {
		t.Errorf("staff back, invalid session: %d", code)
	}
}
