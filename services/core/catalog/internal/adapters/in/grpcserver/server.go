// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the catalog service.
package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

// Server, CatalogService'i uygular.
type Server struct {
	catalogv1.UnimplementedCatalogServiceServer
	svc *app.Service
}

// New connects the service to gRPC.
func New(svc *app.Service) *Server { return &Server{svc: svc} }

func (s *Server) GetByBarcode(ctx context.Context, req *catalogv1.GetByBarcodeRequest) (*catalogv1.GetByBarcodeResponse, error) {
	p, err := s.svc.GetByBarcode(ctx, req.GetBarcode())
	if errors.Is(err, app.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "%s: %s", err, req.GetBarcode())
	}
	if errors.Is(err, domain.ErrNoPrice) {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &catalogv1.GetByBarcodeResponse{Product: toProto(p)}, nil
}

func (s *Server) SearchProducts(ctx context.Context, req *catalogv1.SearchProductsRequest) (*catalogv1.SearchProductsResponse, error) {
	products, err := s.svc.Search(ctx, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	resp := &catalogv1.SearchProductsResponse{}
	for _, p := range products {
		resp.Products = append(resp.Products, toProto(p))
	}
	return resp, nil
}

func (s *Server) GetProduct(ctx context.Context, req *catalogv1.GetProductRequest) (*catalogv1.GetProductResponse, error) {
	p, err := s.svc.GetProduct(ctx, req.GetProductId())
	if errors.Is(err, app.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "%s: %s", err, req.GetProductId())
	}
	if errors.Is(err, domain.ErrNoPrice) {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &catalogv1.GetProductResponse{Product: toProto(p)}, nil
}

func (s *Server) ListMenu(ctx context.Context, _ *catalogv1.ListMenuRequest) (*catalogv1.ListMenuResponse, error) {
	products, err := s.svc.Menu(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	resp := &catalogv1.ListMenuResponse{}
	for _, p := range products {
		resp.Products = append(resp.Products, toProto(p))
	}
	return resp, nil
}

func (s *Server) GetImage(ctx context.Context, req *catalogv1.GetImageRequest) (*catalogv1.GetImageResponse, error) {
	img, err := s.svc.Image(ctx, req.GetProductId())
	if errors.Is(err, app.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "görsel yok: %s", req.GetProductId())
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &catalogv1.GetImageResponse{Sha256: img.SHA256, ContentType: img.ContentType, Data: img.Data}, nil
}

func toProto(p domain.Product) *catalogv1.Product {
	unit := catalogv1.Unit_UNIT_PIECE
	if p.Unit == domain.Kg {
		unit = catalogv1.Unit_UNIT_KG
	}
	out := &catalogv1.Product{
		Id:             p.ID,
		Name:           p.Name,
		Unit:           unit,
		UnitPriceKurus: int64(p.UnitPrice),
		TaxRateBp:      int64(p.TaxRate),
		Barcodes:       p.Barcodes,
		QuickKey:       p.QuickKey,
		Category:       p.Category,
		MenuOrder:      int32(p.MenuOrder),
		Station:        p.Station,
		ImageSha256:    p.ImageSHA256,
		ImageType:      p.ImageType,
	}
	for _, g := range p.Options {
		pg := &catalogv1.OptionGroup{Id: g.ID, Name: g.Name, Min: int32(g.Min), Max: int32(g.Max)}
		for _, o := range g.Options {
			pg.Options = append(pg.Options, &catalogv1.Option{Id: o.ID, Name: o.Name, PriceDeltaKurus: int64(o.PriceDelta)})
		}
		out.OptionGroups = append(out.OptionGroups, pg)
	}
	return out
}
