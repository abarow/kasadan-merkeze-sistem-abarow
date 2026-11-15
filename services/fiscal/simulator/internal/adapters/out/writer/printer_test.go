// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package writer_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"abarow.com/services/fiscal/simulator/internal/adapters/out/writer"
)

func TestWriterPrinterAndDrawer(t *testing.T) {
	var buf bytes.Buffer
	p := writer.New(&buf)
	if err := p.Print(context.Background(), "başlık", "satır 1\nsatır 2\n"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "satır 1\nsatır 2\n" {
		t.Errorf("output %q", buf.String())
	}

	var logs bytes.Buffer
	writer.Drawer{Log: slog.New(slog.NewTextHandler(&logs, nil))}.Open(context.Background(), "nakit satış")
	if !strings.Contains(logs.String(), "nakit satış") {
		t.Errorf("log: %q", logs.String())
	}
}
