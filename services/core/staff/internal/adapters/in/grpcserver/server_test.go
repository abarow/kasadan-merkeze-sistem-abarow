// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/core/staff/internal/adapters/in/grpcserver"
	"abarow.com/services/core/staff/internal/adapters/out/bcrypthasher"
	"abarow.com/services/core/staff/internal/adapters/out/sqlite"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

type printer struct{ titles []string }

func (p *printer) Columns(context.Context) (int, error) { return 32, nil }
func (p *printer) Print(_ context.Context, title string, _ []string) error {
	p.titles = append(p.titles, title)
	return nil
}

type drawer struct{ opened []string }

func (d *drawer) Open(_ context.Context, reason string) error {
	d.opened = append(d.opened, reason)
	return nil
}

type env struct {
	staffv1.StaffServiceClient
	printer *printer
	drawer  *drawer
}

func start(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	auth := app.NewAuth(st, bcrypthasher.Hasher{Cost: bcrypt.MinCost}, time.Hour)
	if _, err := auth.Seed(ctx, []app.SeedCashier{
		{ID: "ayse", Name: "Kasiyer Ayşe", Role: domain.RoleCashier, PIN: "1111"},
		{ID: "ali", Name: "Müdür Ali", Role: domain.RoleManager, PIN: "9999"},
	}); err != nil {
		t.Fatal(err)
	}
	shifts := app.NewShiftService(st, st, app.Events{})
	d := &drawer{}
	shifts.Drawer = d
	svc := app.NewService(st, st, st, app.Events{})
	p := &printer{}
	print := app.NewPrintService(svc, shifts, p, time.UTC)

	conn := grpctest.Conn(t, func(s *grpc.Server) {
		staffv1.RegisterStaffServiceServer(s, grpcserver.New(svc, auth, shifts, print))
	})
	return env{StaffServiceClient: staffv1.NewStaffServiceClient(conn), printer: p, drawer: d}
}

func code(err error) codes.Code { return status.Code(err) }

func login(t *testing.T, c staffv1.StaffServiceClient, id, pin string) string {
	t.Helper()
	r, err := c.Login(context.Background(), &staffv1.LoginRequest{CashierId: id, Pin: pin})
	if err != nil {
		t.Fatal(err)
	}
	return r.GetToken()
}

func TestSession(t *testing.T) {
	e := start(t)
	ctx := context.Background()

	list, err := e.ListCashiers(ctx, &staffv1.ListCashiersRequest{})
	if err != nil || len(list.GetCashiers()) != 2 {
		t.Fatalf("ListCashiers: %v %v", list, err)
	}
	roles := map[string]staffv1.Role{}
	for _, c := range list.GetCashiers() {
		roles[c.GetId()] = c.GetRole()
	}
	if roles["ali"] != staffv1.Role_ROLE_MANAGER || roles["ayse"] != staffv1.Role_ROLE_CASHIER {
		t.Errorf("roller: %v", roles)
	}

	if _, err := e.Login(ctx, &staffv1.LoginRequest{CashierId: "ayse", Pin: "0000"}); code(err) != codes.Unauthenticated {
		t.Errorf("wrong PIN: %v", err)
	}
	if _, err := e.Login(ctx, &staffv1.LoginRequest{CashierId: "yok", Pin: "1111"}); code(err) != codes.NotFound {
		t.Errorf("olmayan kasiyer: %v", err)
	}
	token := login(t, e, "ayse", "1111")
	v, err := e.ValidateSession(ctx, &staffv1.ValidateSessionRequest{Token: token})
	if err != nil || v.GetCashier().GetId() != "ayse" {
		t.Fatalf("ValidateSession: %v %v", v, err)
	}
	if _, err := e.Logout(ctx, &staffv1.LogoutRequest{Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateSession(ctx, &staffv1.ValidateSessionRequest{Token: token}); code(err) != codes.NotFound && code(err) != codes.Unauthenticated {
		t.Errorf("session after sign-out: %v", err)
	}
	// After five wrong attempts the cashier is locked.
	var last error
	for i := 0; i < 5; i++ {
		_, last = e.Login(ctx, &staffv1.LoginRequest{CashierId: "ali", Pin: "0000"})
	}
	if _, err := e.Login(ctx, &staffv1.LoginRequest{CashierId: "ali", Pin: "9999"}); code(err) != codes.ResourceExhausted {
		t.Errorf("locked cashier: %v (last wrong attempt: %v)", err, last)
	}
}

func TestAuthorizeAndApprove(t *testing.T) {
	e := start(t)
	ctx := context.Background()

	if _, err := e.Authorize(ctx, &staffv1.AuthorizeRequest{CashierId: "ali", Action: staffv1.Action_ACTION_UNSPECIFIED}); code(err) != codes.InvalidArgument {
		t.Errorf("unknown operation: %v", err)
	}
	if _, err := e.Approve(ctx, &staffv1.ApproveRequest{ManagerId: "ali", Pin: "9999"}); code(err) != codes.InvalidArgument {
		t.Errorf("unknown operation in approval: %v", err)
	}
	if r, err := e.Authorize(ctx, &staffv1.AuthorizeRequest{CashierId: "ali", Action: staffv1.Action_ACTION_EDIT_FLOOR}); err != nil || r.GetManagerId() != "ali" {
		t.Errorf("manager with their own authority: %v %v", r, err)
	}
	if _, err := e.Authorize(ctx, &staffv1.AuthorizeRequest{CashierId: "ayse", Action: staffv1.Action_ACTION_EDIT_FLOOR}); code(err) != codes.PermissionDenied {
		t.Errorf("cashier doing a manager operation: %v", err)
	}

	// Approval cannot be obtained with a wrong PIN (PermissionDenied); the right PIN gives a single-use key.
	if _, err := e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ali", Pin: "0000", Action: staffv1.Action_ACTION_VOID_ORDER_LINE}); code(err) != codes.PermissionDenied {
		t.Errorf("approval with a wrong PIN: %v", err)
	}
	if _, err := e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ayse", Pin: "1111", Action: staffv1.Action_ACTION_VOID_ORDER_LINE}); code(err) != codes.PermissionDenied {
		t.Errorf("kasiyerden onay: %v", err)
	}
	ap, err := e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ali", Pin: "9999", Action: staffv1.Action_ACTION_VOID_ORDER_LINE})
	if err != nil || ap.GetApprovalToken() == "" || ap.GetExpiresAt() == nil {
		t.Fatalf("Approve: %v %v", ap, err)
	}
	req := &staffv1.AuthorizeRequest{CashierId: "ayse", ApprovalToken: ap.GetApprovalToken(), Action: staffv1.Action_ACTION_VOID_ORDER_LINE}
	if r, err := e.Authorize(ctx, req); err != nil || r.GetManagerId() != "ali" {
		t.Errorf("onayla yetki: %v %v", r, err)
	}
	if _, err := e.Authorize(ctx, req); code(err) != codes.PermissionDenied {
		t.Errorf("used approval: %v", err)
	}
}

func TestShiftCashAndDay(t *testing.T) {
	e := start(t)
	ctx := context.Background()

	cur, err := e.GetCurrentShift(ctx, &staffv1.GetCurrentShiftRequest{})
	if err != nil || cur.GetShift() != nil {
		t.Fatalf("vardiya yokken: %v %v", cur, err)
	}
	// There is no cash movement or closing without a shift being opened.
	if _, err := e.CloseShift(ctx, &staffv1.CloseShiftRequest{CashierId: "ayse"}); code(err) != codes.FailedPrecondition {
		t.Errorf("closing without a shift: %v", err)
	}

	open, err := e.OpenShift(ctx, &staffv1.OpenShiftRequest{CashierId: "ayse", OpeningCashKurus: 50000})
	if err != nil || open.GetShift().GetCashierId() != "ayse" || open.GetShift().GetOpeningCashKurus() != 50000 {
		t.Fatalf("OpenShift: %v %v", open, err)
	}
	if _, err := e.OpenShift(ctx, &staffv1.OpenShiftRequest{CashierId: "ali"}); code(err) != codes.FailedPrecondition {
		t.Errorf("ikinci vardiya: %v", err)
	}
	if cur, _ := e.GetCurrentShift(ctx, &staffv1.GetCurrentShiftRequest{}); cur.GetShift().GetId() != open.GetShift().GetId() {
		t.Errorf("current shift: %v", cur)
	}

	// Putting cash into the register is free; taking it out asks for manager approval.
	in, err := e.RecordCashMovement(ctx, &staffv1.RecordCashMovementRequest{
		CashierId: "ayse", Kind: staffv1.CashMovementKind_CASH_MOVEMENT_KIND_IN, AmountKurus: 10000, Note: "Bozuk para"})
	if err != nil || in.GetMovement().GetAmountKurus() != 10000 || in.GetMovement().GetCashierId() != "ayse" {
		t.Fatalf("cash in: %v %v", in, err)
	}
	out := &staffv1.RecordCashMovementRequest{CashierId: "ayse", Kind: staffv1.CashMovementKind_CASH_MOVEMENT_KIND_OUT, AmountKurus: 2000, Note: "Avans"}
	if _, err := e.RecordCashMovement(ctx, out); code(err) != codes.PermissionDenied {
		t.Errorf("cash out without approval: %v", err)
	}
	ap, err := e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ali", Pin: "9999", Action: staffv1.Action_ACTION_CASH_OUT})
	if err != nil {
		t.Fatal(err)
	}
	out.ApprovalToken = ap.GetApprovalToken()
	if r, err := e.RecordCashMovement(ctx, out); err != nil || r.GetMovement().GetCashierId() != "ali" {
		t.Errorf("cash out with approval: %v %v", r, err)
	}
	if _, err := e.RecordCashMovement(ctx, &staffv1.RecordCashMovementRequest{CashierId: "ayse", AmountKurus: 1}); code(err) != codes.InvalidArgument {
		t.Errorf("movement of unclear kind: %v", err)
	}
	if _, err := e.RecordCashMovement(ctx, &staffv1.RecordCashMovementRequest{
		CashierId: "ayse", Kind: staffv1.CashMovementKind_CASH_MOVEMENT_KIND_IN, AmountKurus: 0, Note: "x"}); code(err) != codes.InvalidArgument {
		t.Errorf("zero amount: %v", err)
	}

	// The drawer is opened only with manager approval and a reason.
	if _, err := e.OpenDrawer(ctx, &staffv1.OpenDrawerRequest{CashierId: "ayse", Reason: "Bozuk para bozdurma"}); code(err) != codes.PermissionDenied {
		t.Errorf("drawer without approval: %v", err)
	}
	ap, _ = e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ali", Pin: "9999", Action: staffv1.Action_ACTION_OPEN_DRAWER})
	if _, err := e.OpenDrawer(ctx, &staffv1.OpenDrawerRequest{CashierId: "ayse", ApprovalToken: ap.GetApprovalToken(), Reason: ""}); code(err) != codes.InvalidArgument {
		t.Errorf("drawer without a reason: %v", err)
	}
	ap, _ = e.Approve(ctx, &staffv1.ApproveRequest{RequestedBy: "ayse", ManagerId: "ali", Pin: "9999", Action: staffv1.Action_ACTION_OPEN_DRAWER})
	if _, err := e.OpenDrawer(ctx, &staffv1.OpenDrawerRequest{CashierId: "ayse", ApprovalToken: ap.GetApprovalToken(), Reason: "Bozuk para bozdurma"}); err != nil {
		t.Fatalf("drawer with approval: %v", err)
	}
	if len(e.drawer.opened) != 1 {
		t.Errorf("drawer was opened %d times", len(e.drawer.opened))
	}

	// The end of day cannot be taken while a shift is open.
	if _, err := e.CloseDay(ctx, &staffv1.CloseDayRequest{CashierId: "ali"}); code(err) != codes.FailedPrecondition {
		t.Errorf("end of day with an open shift: %v", err)
	}
	// Another cashier cannot close; the owner closes with a count.
	if _, err := e.CloseShift(ctx, &staffv1.CloseShiftRequest{CashierId: "ayse", CountedCashKurus: -1}); err == nil {
		t.Error("a negative count was accepted")
	}
	closed, err := e.CloseShift(ctx, &staffv1.CloseShiftRequest{CashierId: "ayse", CountedCashKurus: 58000})
	if err != nil || closed.GetReport().GetShift().GetCashierId() != "ayse" {
		t.Fatalf("CloseShift: %v %v", closed, err)
	}
	shiftID := closed.GetReport().GetShift().GetId()

	hist, err := e.ListShifts(ctx, &staffv1.ListShiftsRequest{CashierId: "ayse", Limit: 10})
	if err != nil || len(hist.GetShifts()) != 1 || hist.GetShifts()[0].GetId() != shiftID {
		t.Fatalf("ListShifts: %v %v", hist, err)
	}
	if _, err := e.GetShiftReport(ctx, &staffv1.GetShiftReportRequest{CashierId: "ayse", ShiftId: shiftID}); err != nil {
		t.Errorf("owner sees the report: %v", err)
	}
	if _, err := e.GetShiftReport(ctx, &staffv1.GetShiftReportRequest{CashierId: "ali", ShiftId: shiftID}); err != nil {
		t.Errorf("manager sees the report: %v", err)
	}
	if _, err := e.GetShiftReport(ctx, &staffv1.GetShiftReportRequest{CashierId: "ayse", ShiftId: 999}); code(err) != codes.NotFound {
		t.Errorf("olmayan vardiya: %v", err)
	}

	// Reports and the end of day.
	if x, err := e.GetXReport(ctx, &staffv1.GetXReportRequest{}); err != nil || x.GetReport().GetZNo() != 1 {
		t.Errorf("X raporu: %v %v", x, err)
	}
	if _, err := e.CloseDay(ctx, &staffv1.CloseDayRequest{CashierId: "ayse"}); code(err) != codes.PermissionDenied {
		t.Errorf("cashier end of day: %v", err)
	}
	z, err := e.CloseDay(ctx, &staffv1.CloseDayRequest{CashierId: "ali"})
	if err != nil || z.GetReport().GetZNo() != 1 || z.GetReport().GetClosedAt() == nil {
		t.Fatalf("CloseDay: %v %v", z, err)
	}

	if _, err := e.PrintDayReport(ctx, &staffv1.PrintDayReportRequest{ZNo: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PrintDayReport(ctx, &staffv1.PrintDayReportRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PrintDayReport(ctx, &staffv1.PrintDayReportRequest{ZNo: 77}); code(err) != codes.NotFound {
		t.Errorf("olmayan Z: %v", err)
	}
	if _, err := e.PrintShiftReport(ctx, &staffv1.PrintShiftReportRequest{CashierId: "ayse", ShiftId: shiftID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PrintShiftReport(ctx, &staffv1.PrintShiftReportRequest{CashierId: "ayse", ShiftId: 999}); code(err) != codes.NotFound {
		t.Errorf("print of a non-existent shift: %v", err)
	}
	joined := strings.Join(e.printer.titles, "|")
	if !strings.Contains(joined, "Z raporu 1") || !strings.Contains(joined, "X raporu") {
		t.Errorf("printed headers: %v", e.printer.titles)
	}
}
