// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the staff service.
package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/money"
	"abarow.com/services/core/staff/internal/adapters/staffpb"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// Server, StaffService'i uygular.
type Server struct {
	staffv1.UnimplementedStaffServiceServer
	svc    *app.Service
	auth   *app.Auth
	shifts *app.ShiftService
	print  *app.PrintService
}

// New connects the services to gRPC.
func New(svc *app.Service, auth *app.Auth, shifts *app.ShiftService, print *app.PrintService) *Server {
	return &Server{svc: svc, auth: auth, shifts: shifts, print: print}
}

func (s *Server) PrintDayReport(ctx context.Context, req *staffv1.PrintDayReportRequest) (*staffv1.PrintDayReportResponse, error) {
	if err := s.print.PrintDay(ctx, req.GetZNo()); err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.PrintDayReportResponse{}, nil
}

func (s *Server) PrintShiftReport(ctx context.Context, req *staffv1.PrintShiftReportRequest) (*staffv1.PrintShiftReportResponse, error) {
	if err := s.print.PrintShift(ctx, req.GetCashierId(), req.GetShiftId()); err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.PrintShiftReportResponse{}, nil
}

func (s *Server) ListCashiers(ctx context.Context, _ *staffv1.ListCashiersRequest) (*staffv1.ListCashiersResponse, error) {
	cashiers, err := s.auth.List(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &staffv1.ListCashiersResponse{}
	for _, c := range cashiers {
		resp.Cashiers = append(resp.Cashiers, cashierToProto(c))
	}
	return resp, nil
}

func (s *Server) Login(ctx context.Context, req *staffv1.LoginRequest) (*staffv1.LoginResponse, error) {
	token, c, expires, err := s.auth.Login(ctx, req.GetCashierId(), req.GetPin())
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.LoginResponse{Token: token, Cashier: cashierToProto(c), ExpiresAt: timestamppb.New(expires)}, nil
}

func (s *Server) ValidateSession(ctx context.Context, req *staffv1.ValidateSessionRequest) (*staffv1.ValidateSessionResponse, error) {
	c, err := s.auth.Validate(ctx, req.GetToken())
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.ValidateSessionResponse{Cashier: cashierToProto(c)}, nil
}

func (s *Server) Logout(ctx context.Context, req *staffv1.LogoutRequest) (*staffv1.LogoutResponse, error) {
	if err := s.auth.Logout(ctx, req.GetToken()); err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.LogoutResponse{}, nil
}

var actions = map[staffv1.Action]domain.Action{
	staffv1.Action_ACTION_CLOSE_DAY:       domain.ActionCloseDay,
	staffv1.Action_ACTION_CASH_OUT:        domain.ActionCashOut,
	staffv1.Action_ACTION_CLOSE_SHIFT:     domain.ActionCloseShift,
	staffv1.Action_ACTION_REFUND_CHARGED:  domain.ActionRefundCharged,
	staffv1.Action_ACTION_OPEN_DRAWER:     domain.ActionOpenDrawer,
	staffv1.Action_ACTION_VOID_ORDER_LINE: domain.ActionVoidOrderLine,
	staffv1.Action_ACTION_EDIT_FLOOR:      domain.ActionEditFloor,
	staffv1.Action_ACTION_APPLY_UPDATE:    domain.ActionApplyUpdate,
}

func (s *Server) OpenDrawer(ctx context.Context, req *staffv1.OpenDrawerRequest) (*staffv1.OpenDrawerResponse, error) {
	actor, err := s.auth.Actor(ctx, req.GetCashierId(), req.GetApprovalToken(), domain.ActionOpenDrawer)
	if err != nil {
		return nil, toStatus(err)
	}
	if _, err := s.shifts.OpenDrawer(ctx, actor, req.GetCashierId(), req.GetReason()); err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.OpenDrawerResponse{}, nil
}

func (s *Server) Authorize(ctx context.Context, req *staffv1.AuthorizeRequest) (*staffv1.AuthorizeResponse, error) {
	action, ok := actions[req.GetAction()]
	if !ok {
		return nil, toStatus(domain.ErrUnknownAction)
	}
	manager, err := s.auth.Authorize(ctx, req.GetCashierId(), req.GetApprovalToken(), action)
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.AuthorizeResponse{ManagerId: manager}, nil
}

func (s *Server) Approve(ctx context.Context, req *staffv1.ApproveRequest) (*staffv1.ApproveResponse, error) {
	action, ok := actions[req.GetAction()]
	if !ok {
		return nil, toStatus(domain.ErrUnknownAction)
	}
	token, expires, err := s.auth.Approve(ctx, req.GetRequestedBy(), req.GetManagerId(), req.GetPin(), action)
	// A wrong PIN does not invalidate the session here: the screen would close the cashier's
	// session on 401; an approval refusal is 403.
	if errors.Is(err, domain.ErrWrongPIN) {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.ApproveResponse{ApprovalToken: token, ExpiresAt: timestamppb.New(expires)}, nil
}

// toStatus turns domain errors into gRPC codes; the others (database etc.)
// are transient.
func toStatus(err error) error {
	code := codes.Unavailable
	var open domain.ShiftOpenError
	var unfinished domain.UnfinishedSalesError
	switch {
	case errors.Is(err, domain.ErrWrongPIN), errors.Is(err, domain.ErrInvalidSession):
		code = codes.Unauthenticated
	case errors.Is(err, domain.ErrLocked):
		code = codes.ResourceExhausted
	case errors.Is(err, domain.ErrUnknownCashier), errors.Is(err, domain.ErrUnknownShift), errors.Is(err, domain.ErrUnknownPeriod):
		code = codes.NotFound
	case errors.As(err, &open), errors.As(err, &unfinished), errors.Is(err, domain.ErrNoShift), errors.Is(err, domain.ErrShiftNotClosed):
		code = codes.FailedPrecondition
	case errors.Is(err, domain.ErrNotShiftOwner), errors.Is(err, domain.ErrCashOutNotManager),
		errors.Is(err, domain.ErrCloseDayNotManager), errors.Is(err, domain.ErrNotManager),
		errors.Is(err, domain.ErrInvalidApproval), errors.Is(err, domain.ErrManagerRequired),
		errors.Is(err, domain.ErrDrawerNotManager), errors.Is(err, domain.ErrDisabled):
		code = codes.PermissionDenied
	case errors.Is(err, domain.ErrNegativeCash), errors.Is(err, domain.ErrCashAmount),
		errors.Is(err, domain.ErrCashNote), errors.Is(err, domain.ErrCashKind), errors.Is(err, domain.ErrUnknownAction),
		errors.Is(err, domain.ErrDrawerReason):
		code = codes.InvalidArgument
	}
	return status.Error(code, err.Error())
}

func cashierToProto(c domain.Cashier) *staffv1.Cashier {
	role := staffv1.Role_ROLE_CASHIER
	if c.Role == domain.RoleManager {
		role = staffv1.Role_ROLE_MANAGER
	}
	return &staffv1.Cashier{Id: c.ID, Name: c.Name, Role: role}
}

func (s *Server) GetXReport(ctx context.Context, _ *staffv1.GetXReportRequest) (*staffv1.GetXReportResponse, error) {
	r, err := s.svc.XReport(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.GetXReportResponse{Report: toProto(r)}, nil
}

func (s *Server) CloseDay(ctx context.Context, req *staffv1.CloseDayRequest) (*staffv1.CloseDayResponse, error) {
	actor, err := s.auth.Actor(ctx, req.GetCashierId(), req.GetApprovalToken(), domain.ActionCloseDay)
	if err != nil {
		return nil, toStatus(err)
	}
	r, err := s.svc.CloseDay(ctx, actor)
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.CloseDayResponse{Report: toProto(r)}, nil
}

func toProto(r domain.DayReport) *staffv1.DayReport {
	out := &staffv1.DayReport{
		ZNo:              r.ZNo,
		OpenedAt:         staffpb.Timestamp(r.OpenedAt),
		ClosedAt:         staffpb.Timestamp(r.ClosedAt),
		ReceiptCount:     r.ReceiptCount,
		FirstReceiptNo:   r.FirstReceiptNo,
		LastReceiptNo:    r.LastReceiptNo,
		TotalKurus:       int64(r.Total),
		TaxTotalKurus:    int64(r.TaxTotal),
		VoidedCount:      r.VoidedCount,
		VoidedTotalKurus: int64(r.VoidedTotal),
	}
	for _, t := range r.Taxes {
		out.Taxes = append(out.Taxes, &staffv1.TaxTotal{RateBp: int64(t.Rate), GrossKurus: int64(t.Gross), TaxKurus: int64(t.Tax)})
	}
	out.Payments = staffpb.Payments(r.Payments)
	return out
}

func (s *Server) GetCurrentShift(ctx context.Context, _ *staffv1.GetCurrentShiftRequest) (*staffv1.GetCurrentShiftResponse, error) {
	sh, err := s.shifts.Current(ctx)
	if errors.Is(err, domain.ErrNoShift) {
		return &staffv1.GetCurrentShiftResponse{}, nil
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.GetCurrentShiftResponse{Shift: staffpb.Shift(sh)}, nil
}

func (s *Server) OpenShift(ctx context.Context, req *staffv1.OpenShiftRequest) (*staffv1.OpenShiftResponse, error) {
	sh, err := s.shifts.Open(ctx, req.GetCashierId(), money.Money(req.GetOpeningCashKurus()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.OpenShiftResponse{Shift: staffpb.Shift(sh)}, nil
}

func (s *Server) CloseShift(ctx context.Context, req *staffv1.CloseShiftRequest) (*staffv1.CloseShiftResponse, error) {
	actor, err := s.auth.Actor(ctx, req.GetCashierId(), req.GetApprovalToken(), domain.ActionCloseShift)
	if err != nil {
		return nil, toStatus(err)
	}
	r, err := s.shifts.Close(ctx, actor, money.Money(req.GetCountedCashKurus()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.CloseShiftResponse{Report: staffpb.ShiftReport(r)}, nil
}

func (s *Server) ListShifts(ctx context.Context, req *staffv1.ListShiftsRequest) (*staffv1.ListShiftsResponse, error) {
	shifts, err := s.shifts.History(ctx, req.GetCashierId(), int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &staffv1.ListShiftsResponse{}
	for _, sh := range shifts {
		resp.Shifts = append(resp.Shifts, staffpb.Shift(sh))
	}
	return resp, nil
}

func (s *Server) GetShiftReport(ctx context.Context, req *staffv1.GetShiftReportRequest) (*staffv1.GetShiftReportResponse, error) {
	r, err := s.shifts.Report(ctx, req.GetCashierId(), req.GetShiftId())
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.GetShiftReportResponse{Report: staffpb.ShiftReport(r)}, nil
}

var movementKinds = map[staffv1.CashMovementKind]domain.CashMovementKind{
	staffv1.CashMovementKind_CASH_MOVEMENT_KIND_IN:  domain.CashIn,
	staffv1.CashMovementKind_CASH_MOVEMENT_KIND_OUT: domain.CashOut,
}

func (s *Server) RecordCashMovement(ctx context.Context, req *staffv1.RecordCashMovementRequest) (*staffv1.RecordCashMovementResponse, error) {
	kind, ok := movementKinds[req.GetKind()]
	if !ok {
		return nil, toStatus(domain.ErrCashKind)
	}
	actor, err := s.auth.Actor(ctx, req.GetCashierId(), req.GetApprovalToken(), domain.ActionCashOut)
	if err != nil {
		return nil, toStatus(err)
	}
	m, err := s.shifts.RecordCashMovement(ctx, actor, kind, money.Money(req.GetAmountKurus()), req.GetNote())
	if err != nil {
		return nil, toStatus(err)
	}
	return &staffv1.RecordCashMovementResponse{Movement: &staffv1.CashMovement{
		Id:          m.ID,
		ShiftId:     m.ShiftID,
		Kind:        req.GetKind(),
		AmountKurus: int64(m.Amount),
		Note:        m.Note,
		CashierId:   m.By,
		At:          staffpb.Timestamp(m.At),
	}}, nil
}
