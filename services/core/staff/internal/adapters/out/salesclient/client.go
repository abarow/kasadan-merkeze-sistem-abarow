// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package salesclient is the adapter for what is asked of sales before closing:
// the sale events not yet published (app.EventBacklog) and the sales whose payment was
// taken but whose receipt could not be printed (app.ChargedSales).
package salesclient

import (
	"context"
	"time"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/core/staff/internal/app"
)

// queryTimeout is the time limit of the short questions asked of sales: if sales is
// frozen the closing does not wait forever, it returns an error.
var queryTimeout = 5 * time.Second

// Outbox counts the pending events in sales' outbox.
type Outbox struct {
	Client salesv1.SalesServiceClient
}

var _ app.EventBacklog = Outbox{}

func (o Outbox) PendingEvents(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	resp, err := o.Client.GetOutboxStatus(ctx, &salesv1.GetOutboxStatusRequest{})
	return resp.GetPending(), service.RemoteErrorFrom("satış servisi", err)
}

// Charged counts the sales whose payment was taken but whose fiscal record could not be made.
type Charged struct {
	Client salesv1.SalesServiceClient
}

var _ app.ChargedSales = Charged{}

func (c Charged) CountCharged(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	resp, err := c.Client.ListSales(ctx, &salesv1.ListSalesRequest{Status: salesv1.SaleStatus_SALE_STATUS_CHARGED, Limit: 100})
	return int64(len(resp.GetSales())), service.RemoteErrorFrom("satış servisi", err)
}
