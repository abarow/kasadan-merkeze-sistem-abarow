// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	devicev1 "abarow.com/gen/com/abarow/device/v1"
)

var (
	// scanPing is the interval of the empty line that keeps the connection alive.
	scanPing = 25 * time.Second
	// scanRetry is the retry interval if device cannot be reached or the stream breaks;
	// the screen's connection stays open meanwhile.
	scanRetry = 2 * time.Second
)

// printerStates are the names of the printer states on the screen.
var printerStates = map[devicev1.PrinterState]string{
	devicev1.PrinterState_PRINTER_STATE_UNSPECIFIED:    "unknown",
	devicev1.PrinterState_PRINTER_STATE_READY:          "ready",
	devicev1.PrinterState_PRINTER_STATE_PAPER_NEAR_END: "paper_near_end",
	devicev1.PrinterState_PRINTER_STATE_PAPER_OUT:      "paper_out",
	devicev1.PrinterState_PRINTER_STATE_COVER_OPEN:     "cover_open",
	devicev1.PrinterState_PRINTER_STATE_OFFLINE:        "offline",
	devicev1.PrinterState_PRINTER_STATE_ERROR:          "error",
}

// sseEvent is an event that goes to the screen.
type sseEvent struct {
	name string
	data any
}

// scans is the screen's peripheral event stream (Server-Sent Events): every
// barcode read is a "scan" event ({"code": "..."}), the printer's state is a
// "printer" event ({"state": "paper_out", "message": "Kağıt bitti"}; first
// the current state, then every change). The screen opens the stream with the cashier session
// (the Authorization header). If device cannot be reached or the stream breaks
// the connection stays open and is retried.
func (h *handler) scans(w http.ResponseWriter, r *http.Request) {
	if h.device == nil {
		writeError(w, http.StatusNotFound, "barkod okuyucu yapılandırılmamış (ABAROW_DEVICE_ADDR)")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "akış desteklenmiyor")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": abarow\n\n")
	flusher.Flush()

	ctx := r.Context()
	ping := time.NewTicker(scanPing)
	defer ping.Stop()
	for ctx.Err() == nil {
		h.relayDevice(ctx, w, flusher, ping)
		select {
		case <-ctx.Done():
		case <-time.After(scanRetry):
		}
	}
}

// relayDevice passes device's read and printer streams to the screen; if the read
// stream breaks it returns (the two are rebuilt together). On an old device that
// does not know the printer state (UNIMPLEMENTED) only the reads flow: every
// service is updated on its own.
func (h *handler) relayDevice(ctx context.Context, w io.Writer, flusher http.Flusher, ping *time.Ticker) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	scans, err := h.device.WatchScans(ctx, &devicev1.WatchScansRequest{})
	if err != nil {
		return
	}
	events, ended := make(chan sseEvent), make(chan struct{})
	send := func(e sseEvent) bool {
		select {
		case events <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(ended)
		for {
			m, err := scans.Recv()
			if err != nil || !send(sseEvent{"scan", map[string]string{"code": m.GetCode()}}) {
				return
			}
		}
	}()
	go func() {
		printer, err := h.device.WatchPrinter(ctx, &devicev1.WatchPrinterRequest{})
		if err != nil {
			return
		}
		for {
			m, err := printer.Recv()
			if err != nil {
				return
			}
			data := map[string]string{"state": printerStates[m.GetState()], "message": m.GetMessage()}
			if !send(sseEvent{"printer", data}) {
				return
			}
		}
	}()
	for {
		select {
		case e := <-events:
			data, _ := json.Marshal(e.data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, data)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ended:
			return
		case <-ctx.Done():
			return
		}
	}
}

// simulateScan imitates a scan on the simulator reader (trial and tests):
// {"code": "..."}. 409 on a real reader.
func (h *handler) simulateScan(w http.ResponseWriter, r *http.Request) {
	if h.device == nil {
		writeError(w, http.StatusNotFound, "barkod okuyucu yapılandırılmamış (ABAROW_DEVICE_ADDR)")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı")
		return
	}
	if _, err := h.device.SimulateScan(r.Context(), &devicev1.SimulateScanRequest{Code: body.Code}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// simulatePrinter sets the simulator printer's state (trial and tests):
// {"state": "paper_out"}. 409 on a real printer.
func (h *handler) simulatePrinter(w http.ResponseWriter, r *http.Request) {
	if h.device == nil {
		writeError(w, http.StatusNotFound, "yazıcı yapılandırılmamış (ABAROW_DEVICE_ADDR)")
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı")
		return
	}
	state, ok := devicev1.PrinterState(-1), false
	for s, name := range printerStates {
		if name == body.State && s != devicev1.PrinterState_PRINTER_STATE_UNSPECIFIED {
			state, ok = s, true
		}
	}
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "yazıcı durumu geçersiz (ready, paper_near_end, paper_out, cover_open, offline, error)")
		return
	}
	if _, err := h.device.SimulatePrinter(r.Context(), &devicev1.SimulatePrinterRequest{State: state}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// simulateWeight sets the simulator scale's weight (trial and tests):
// {"grams": 1250, "moving": false}. 409 on a real scale.
func (h *handler) simulateWeight(w http.ResponseWriter, r *http.Request) {
	if h.device == nil {
		writeError(w, http.StatusNotFound, "terazi yapılandırılmamış (ABAROW_DEVICE_ADDR)")
		return
	}
	var body struct {
		Grams  int64 `json:"grams"`
		Moving bool  `json:"moving"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı")
		return
	}
	if _, err := h.device.SimulateWeight(r.Context(), &devicev1.SimulateWeightRequest{Grams: body.Grams, Moving: body.Moving}); err != nil {
		replyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
