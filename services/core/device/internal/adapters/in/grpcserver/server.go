// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the device service.
package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

// Server, DeviceService'i uygular.
type Server struct {
	devicev1.UnimplementedDeviceServiceServer
	svc *app.Service
}

// New connects the service to gRPC.
func New(svc *app.Service) *Server { return &Server{svc: svc} }

func (s *Server) Print(ctx context.Context, req *devicev1.PrintRequest) (*devicev1.PrintResponse, error) {
	err := s.svc.Print(ctx, domain.Document{Title: req.GetTitle(), Lines: req.GetLines()}, req.GetStation())
	if errors.Is(err, domain.ErrInvalidDocument) {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "yazıcı: "+err.Error())
	}
	return &devicev1.PrintResponse{}, nil
}

func (s *Server) OpenDrawer(ctx context.Context, req *devicev1.OpenDrawerRequest) (*devicev1.OpenDrawerResponse, error) {
	if err := s.svc.OpenDrawer(ctx, req.GetReason()); err != nil {
		return nil, status.Error(codes.Unavailable, "çekmece: "+err.Error())
	}
	return &devicev1.OpenDrawerResponse{}, nil
}

func (s *Server) GetStatus(context.Context, *devicev1.GetStatusRequest) (*devicev1.GetStatusResponse, error) {
	st := s.svc.Status()
	return &devicev1.GetStatusResponse{
		Printer: st.Printer, Columns: int32(st.Columns), Printed: st.Printed, DrawerOpened: st.DrawerOpened, Scanner: st.Scanner,
		PrinterState: printerStates[st.PrinterState], PrinterMessage: st.PrinterState.Message(),
		Scale: st.Scale, Weighed: st.Weighed,
	}, nil
}

// scaleRefusals are the situations in which the scale gives no weight and which the cashier
// can correct (FAILED_PRECONDITION; the message shows on the screen).
var scaleRefusals = []error{
	domain.ErrScaleMotion, domain.ErrScaleEmpty, domain.ErrScaleNegative, domain.ErrScaleOver,
	domain.ErrScaleFault, domain.ErrScaleUnits, domain.ErrScaleReply,
}

func (s *Server) ReadWeight(ctx context.Context, _ *devicev1.ReadWeightRequest) (*devicev1.ReadWeightResponse, error) {
	g, err := s.svc.Weigh(ctx)
	if err == nil {
		return &devicev1.ReadWeightResponse{Grams: g}, nil
	}
	if errors.Is(err, domain.ErrNoScale) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	for _, r := range scaleRefusals {
		if errors.Is(err, r) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	return nil, status.Error(codes.Unavailable, err.Error())
}

func (s *Server) SimulateWeight(_ context.Context, req *devicev1.SimulateWeightRequest) (*devicev1.SimulateWeightResponse, error) {
	if err := s.svc.SimulateWeight(domain.Reading{Grams: req.GetGrams(), Stable: !req.GetMoving()}); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &devicev1.SimulateWeightResponse{}, nil
}

// printerStates are the contract counterparts of the domain states.
var printerStates = map[domain.PrinterState]devicev1.PrinterState{
	domain.PrinterUnknown:      devicev1.PrinterState_PRINTER_STATE_UNSPECIFIED,
	domain.PrinterReady:        devicev1.PrinterState_PRINTER_STATE_READY,
	domain.PrinterPaperNearEnd: devicev1.PrinterState_PRINTER_STATE_PAPER_NEAR_END,
	domain.PrinterPaperOut:     devicev1.PrinterState_PRINTER_STATE_PAPER_OUT,
	domain.PrinterCoverOpen:    devicev1.PrinterState_PRINTER_STATE_COVER_OPEN,
	domain.PrinterOffline:      devicev1.PrinterState_PRINTER_STATE_OFFLINE,
	domain.PrinterError:        devicev1.PrinterState_PRINTER_STATE_ERROR,
}

// WatchPrinter streams the printer's state (first the current one, then the changes)
// until the client leaves.
func (s *Server) WatchPrinter(_ *devicev1.WatchPrinterRequest, stream grpc.ServerStreamingServer[devicev1.WatchPrinterResponse]) error {
	for st := range s.svc.WatchPrinter(stream.Context()) {
		if err := stream.Send(&devicev1.WatchPrinterResponse{State: printerStates[st], Message: st.Message()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) SimulatePrinter(ctx context.Context, req *devicev1.SimulatePrinterRequest) (*devicev1.SimulatePrinterResponse, error) {
	st := domain.PrinterState(-1)
	for d, p := range printerStates {
		if p == req.GetState() {
			st = d
		}
	}
	err := s.svc.SimulatePrinter(ctx, st)
	switch {
	case errors.Is(err, domain.ErrNoPrinterSimulator):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrInvalidPrinterState):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &devicev1.SimulatePrinterResponse{}, nil
}

// WatchScans streams the codes read until the client leaves.
func (s *Server) WatchScans(_ *devicev1.WatchScansRequest, stream grpc.ServerStreamingServer[devicev1.WatchScansResponse]) error {
	for code := range s.svc.Watch(stream.Context()) {
		if err := stream.Send(&devicev1.WatchScansResponse{Code: code}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) SimulateScan(_ context.Context, req *devicev1.SimulateScanRequest) (*devicev1.SimulateScanResponse, error) {
	err := s.svc.Simulate(req.GetCode())
	switch {
	case errors.Is(err, domain.ErrNoSimulator):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrInvalidScan):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &devicev1.SimulateScanResponse{}, nil
}
