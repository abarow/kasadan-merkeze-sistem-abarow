// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the rules of the documents that go to the peripherals.
package domain

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// MaxLines is the largest number of lines in a single document; a huge document sent by
// mistake should not keep the printer busy for minutes.
const MaxLines = 2000

var (
	// ErrInvalidDocument wraps every error Validate returns.
	ErrInvalidDocument = errors.New("belge basılamaz")
	ErrEmptyDocument   = errors.New("boş belge")
)

// LineTooWideError reports a line that exceeds the printer's column count. The line
// is not silently cut or wrapped: the amount could shift on the receipt.
type LineTooWideError struct {
	Line    int // starts from 1
	Width   int
	Columns int
}

func (e LineTooWideError) Error() string {
	return fmt.Sprintf("%d. satır %d karakter; yazıcı %d sütun", e.Line, e.Width, e.Columns)
}

// Document is a plain text document sent to the printer (receipt, report). The text
// is UTF-8; converting it to the printer's code page is the printer adapter's job.
type Document struct {
	// Title is the name that shows in the logs.
	Title string
	Lines []string
}

// Validate checks that the document can be printed on a printer with columns columns;
// the errors wrap ErrInvalidDocument.
func (d Document) Validate(columns int) error {
	if err := d.check(columns); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	return nil
}

func (d Document) check(columns int) error {
	if len(d.Lines) == 0 {
		return ErrEmptyDocument
	}
	if len(d.Lines) > MaxLines {
		return fmt.Errorf("belge %d satır; en çok %d", len(d.Lines), MaxLines)
	}
	for i, l := range d.Lines {
		if !utf8.ValidString(l) {
			return fmt.Errorf("%d. satır geçerli UTF-8 değil", i+1)
		}
		if w := utf8.RuneCountInString(l); w > columns {
			return LineTooWideError{Line: i + 1, Width: w, Columns: columns}
		}
	}
	return nil
}
