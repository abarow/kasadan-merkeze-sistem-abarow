// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestDocumentValidate(t *testing.T) {
	// Turkish characters are counted as characters, not bytes.
	ok := Document{Title: "Fiş", Lines: []string{strings.Repeat("Ş", 32), ""}}
	if err := ok.Validate(32); err != nil {
		t.Errorf("a line of 32 characters was rejected: %v", err)
	}

	var wide LineTooWideError
	err := Document{Lines: []string{"kısa", strings.Repeat("ğ", 33)}}.Validate(32)
	if !errors.Is(err, ErrInvalidDocument) || !errors.As(err, &wide) || wide.Line != 2 || wide.Width != 33 || wide.Columns != 32 {
		t.Errorf("wide line: %v", err)
	}
	if err := (Document{}).Validate(32); !errors.Is(err, ErrEmptyDocument) {
		t.Errorf("empty document: %v", err)
	}
	if err := (Document{Lines: []string{"\xff"}}).Validate(32); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
	if err := (Document{Lines: make([]string, MaxLines+1)}).Validate(32); err == nil {
		t.Error("a document that is too long was accepted")
	}
}
