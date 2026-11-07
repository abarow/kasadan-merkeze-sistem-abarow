// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"abarow.com/services/update/internal/identity"
)

// Report is the running version state a unit reports to the tier above. The tier
// above shows it to the admin ("which machine is on which version"). The unit signs the
// report with its own device key; the server accepts it only from an approved unit
// and writes the moment the report arrived itself (At).
type Report struct {
	// At is the moment the server received the report (the server writes it; a sent value is ignored).
	At time.Time `json:"at,omitzero"`
	// Version is the last applied version label (empty if never applied).
	Version string `json:"version,omitempty"`
	// Phase is the phase of the new release flow (none, available, downloading, applying, rolled_back).
	Phase string `json:"phase,omitempty"`
	// Offered is the label of the version that has been announced but not applied.
	Offered string `json:"offered,omitempty"`
	// Units are the digests of the units' running images (unit → digest).
	Units map[string]string `json:"units,omitempty"`
}

// Limits.
const (
	maxReportBytes = 16 << 10
	maxReportUnits = 128
	// ReportSkew is how far the report's timestamp may deviate from the server's clock.
	ReportSkew = 5 * time.Minute
)

var (
	unitNameRe = regexp.MustCompile(`^abarow-[a-z0-9][a-z0-9-]*$`)
	digestRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	labelRe    = regexp.MustCompile(`^[A-Za-z0-9._+-]{0,64}$`)
	phaseRe    = regexp.MustCompile(`^[a-z_]{0,32}$`)
)

// ReportMessage is the message signed in the report: the device, the timestamp and the digest of the payload.
func ReportMessage(device string, ts time.Time, payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return []byte(fmt.Sprintf("abarow-durum-v1\n%s\n%s\n%s", device, ts.UTC().Format(time.RFC3339), hex.EncodeToString(sum[:])))
}

// ReportInput is a signed status report.
type ReportInput struct {
	Device    string
	Time      time.Time
	Payload   []byte // Report'un JSON hali
	Signature []byte
}

// SubmitReport verifies and stores an approved unit's status report. The signature is
// verified with the registered device key; the timestamp must be close to the server's clock
// (resending an old report is limited).
func (s *Service) SubmitReport(ctx context.Context, in ReportInput) error {
	r, err := s.approved(ctx, in.Device)
	if err != nil {
		return err
	}
	now := s.now()
	if d := now.Sub(in.Time); d > ReportSkew || d < -ReportSkew {
		return fmt.Errorf("%w: bildirimin zaman damgası sunucu saatinden çok uzak", ErrBadRequest)
	}
	if len(in.Payload) == 0 || len(in.Payload) > maxReportBytes {
		return fmt.Errorf("%w: bildirim boyutu geçersiz", ErrBadRequest)
	}
	if !identity.Verify(r.Public, ReportMessage(in.Device, in.Time, in.Payload), in.Signature) {
		return fmt.Errorf("%w: bildirim imzası geçersiz", ErrBadRequest)
	}
	var rep Report
	dec := json.NewDecoder(bytes.NewReader(in.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		return fmt.Errorf("%w: bildirim okunamadı", ErrBadRequest)
	}
	if !labelRe.MatchString(rep.Version) || !labelRe.MatchString(rep.Offered) || !phaseRe.MatchString(rep.Phase) || len(rep.Units) > maxReportUnits {
		return fmt.Errorf("%w: bildirim alanları geçersiz", ErrBadRequest)
	}
	for u, d := range rep.Units {
		if !unitNameRe.MatchString(u) || !digestRe.MatchString(d) {
			return fmt.Errorf("%w: birim ya da digest geçersiz", ErrBadRequest)
		}
	}
	rep.At = now
	return s.store.SaveReport(ctx, in.Device, rep)
}
