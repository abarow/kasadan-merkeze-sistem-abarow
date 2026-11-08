// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"abarow.com/services/update/internal/identity"
)

const repDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func reportOf(t *testing.T, i *identity.Identity, ts time.Time, rep Report) ReportInput {
	t.Helper()
	payload, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	return ReportInput{Device: i.ID, Time: ts, Payload: payload, Signature: i.Sign(ReportMessage(i.ID, ts, payload))}
}

func TestSubmitReportStoresIt(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "register-1")
	fullFlow(t, o, i)

	in := reportOf(t, i, *o.fakeClock, Report{Version: "0.4.0", Phase: "available", Offered: "0.5.0", Units: map[string]string{"abarow-sales": repDigest}})
	if err := o.svc.SubmitReport(ctx, in); err != nil {
		t.Fatal(err)
	}
	r, err := o.svc.Get(ctx, i.ID)
	if err != nil || r.Report == nil {
		t.Fatalf("no report: %+v %v", r, err)
	}
	if r.Report.Version != "0.4.0" || r.Report.Offered != "0.5.0" || r.Report.Units["abarow-sales"] != repDigest {
		t.Errorf("bildirim: %+v", r.Report)
	}
	// The server writes the time.
	if !r.Report.At.Equal(*o.fakeClock) {
		t.Errorf("received moment %v, want %v", r.Report.At, *o.fakeClock)
	}
	// A registration decision (revocation) does not delete the report.
	if err := o.svc.Revoke(ctx, "yonetici", i.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := o.svc.Get(ctx, i.ID); r.Report == nil || r.Report.Version != "0.4.0" {
		t.Errorf("the cancel deleted the report: %+v", r.Report)
	}
}

func TestSubmitReportRejects(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "register-1")
	other := unitDevice(t, "baska-makine")
	good := Report{Units: map[string]string{"abarow-sales": repDigest}}

	// A unit that is not approved cannot send a report.
	if _, err := o.svc.Submit(ctx, request(i, "register", "pilot")); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.SubmitReport(ctx, reportOf(t, i, *o.fakeClock, good)); !errors.Is(err, ErrPending) {
		t.Errorf("without approval: %v", err)
	}
	if err := o.svc.Approve(ctx, "yonetici", i.ID, identity.Code(i.ID)); err != nil {
		t.Fatal(err)
	}

	bad := func(label string, in ReportInput) {
		t.Helper()
		if err := o.svc.SubmitReport(ctx, in); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: accepted (%v)", label, err)
		}
	}
	// Signature with another device's key.
	forged := reportOf(t, other, *o.fakeClock, good)
	forged.Device = i.ID
	bad("başkasının imzası", forged)
	// The payload was changed after the signature.
	tampered := reportOf(t, i, *o.fakeClock, good)
	tampered.Payload = []byte(strings.Replace(string(tampered.Payload), "1111", "2222", 1))
	bad("değiştirilmiş yük", tampered)
	// Timestamp: old and future.
	bad("eski zaman damgası", reportOf(t, i, o.fakeClock.Add(-ReportSkew-time.Minute), good))
	bad("ileri zaman damgası", reportOf(t, i, o.fakeClock.Add(ReportSkew+time.Minute), good))
	// Invalid content.
	bad("birim adı", reportOf(t, i, *o.fakeClock, Report{Units: map[string]string{"sshd": repDigest}}))
	bad("digest", reportOf(t, i, *o.fakeClock, Report{Units: map[string]string{"abarow-sales": "sha256:kısa"}}))
	bad("sürüm etiketi", reportOf(t, i, *o.fakeClock, Report{Version: "<script>"}))
	many := map[string]string{}
	for n := 0; n < maxReportUnits+1; n++ {
		many["abarow-u"+strings.Repeat("a", n%5)+string(rune('a'+n%26))+strings.Repeat("b", n/26)] = repDigest
	}
	bad("çok birim", reportOf(t, i, *o.fakeClock, Report{Units: many}))
	// Bilinmeyen alan.
	raw := []byte(`{"units":{},"fazla":1}`)
	bad("bilinmeyen alan", ReportInput{Device: i.ID, Time: *o.fakeClock, Payload: raw, Signature: i.Sign(ReportMessage(i.ID, *o.fakeClock, raw))})
	// None of them changed the registration.
	if r, _ := o.svc.Get(ctx, i.ID); r.Report != nil {
		t.Errorf("an invalid report was stored: %+v", r.Report)
	}
	// Bilinmeyen cihaz.
	ghost := reportOf(t, other, *o.fakeClock, good)
	if err := o.svc.SubmitReport(ctx, ghost); !errors.Is(err, ErrNotFound) {
		t.Errorf("unregistered device: %v", err)
	}
}
