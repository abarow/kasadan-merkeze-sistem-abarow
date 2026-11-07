// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package serialscale

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"abarow.com/services/core/device/internal/domain"
)

// Protocol is the scale's serial port language: the weight query, the end of the
// reply and decoding the reply. Most register scales speak one of these two
// protocols or can be switched to one in their settings.
type Protocol struct {
	Name    string
	command []byte
	end     byte // the last byte of the reply
	parse   func([]byte) (domain.Reading, error)
}

const (
	stx = 0x02
	etx = 0x03
	lf  = 0x0a
	cr  = 0x0d
)

// Toledo8217: "W" is asked; the reply is <STX>weight<CR> (e.g. "01.250", with N at the end for
// net weight) or, if no weight can be given, <STX>?status<CR>. Status
// byte: bit 0 motion, 1 over capacity, 2 negative, 3 outside the zero range,
// 4 exact zero, 5 net weight. The weight is in kilograms (the scale is set to kg).
var Toledo8217 = Protocol{Name: "toledo8217", command: []byte("W"), end: cr, parse: parseToledo}

// NCI is the Weigh-Tronix/NCI ECR protocol: "W<CR>" is asked; the reply is
// <LF>weightUNIT<CR><LF>Shh<CR><ETX> (e.g. "01.250KG") or, if no weight can be
// given, just <LF>Shh<CR><ETX>. Status bytes: 1st byte bit 0
// motion, 1 at zero, 2 RAM, 3 EEPROM error; 2nd byte bit 0 under capacity
// (negative), 1 over capacity, 2 ROM, 3 calibration error, 6 a third byte
// exists; 3rd byte bit 3 reset-at-power-on error.
var NCI = Protocol{Name: "nci", command: []byte("W\r"), end: etx, parse: parseNCI}

// ParseProtocol is the protocol name in the setting.
func ParseProtocol(name string) (Protocol, error) {
	switch strings.ToLower(name) {
	case Toledo8217.Name:
		return Toledo8217, nil
	case NCI.Name:
		return NCI, nil
	}
	return Protocol{}, fmt.Errorf("terazi protokolü %q bilinmiyor (toledo8217 ya da nci)", name)
}

// grams turns a kilogram value like "01.250" into grams. The decimal point
// is mandatory: the unit of a value without a point is not clear.
func grams(s string) (int64, error) {
	whole, frac, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok || whole == "" || len(frac) > 3 {
		return 0, domain.ErrScaleReply
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, domain.ErrScaleReply
	}
	f := int64(0)
	if frac != "" {
		f, err = strconv.ParseInt(frac+strings.Repeat("0", 3-len(frac)), 10, 64)
		if err != nil || f < 0 {
			return 0, domain.ErrScaleReply
		}
	}
	return w*1000 + f, nil
}

func parseToledo(reply []byte) (domain.Reading, error) {
	i := bytes.IndexByte(reply, stx)
	if i < 0 || !bytes.HasSuffix(reply, []byte{cr}) {
		return domain.Reading{}, domain.ErrScaleReply
	}
	body := reply[i+1 : len(reply)-1]
	if q := bytes.IndexByte(body, '?'); q >= 0 {
		if q+1 >= len(body) {
			return domain.Reading{}, domain.ErrScaleReply
		}
		st := body[q+1]
		switch {
		case st&0x02 != 0:
			return domain.Reading{}, domain.ErrScaleOver
		case st&0x04 != 0:
			return domain.Reading{}, domain.ErrScaleNegative
		case st&0x08 != 0:
			return domain.Reading{}, domain.ErrScaleFault
		case st&0x01 != 0:
			return domain.Reading{Stable: false}, nil
		case st&0x10 != 0:
			return domain.Reading{Stable: true}, nil
		}
		return domain.Reading{}, domain.ErrScaleReply
	}
	g, err := grams(strings.TrimSuffix(string(body), "N"))
	if err != nil {
		return domain.Reading{}, err
	}
	return domain.Reading{Grams: g, Stable: true}, nil
}

func parseNCI(reply []byte) (domain.Reading, error) {
	var weight, status string
	for _, f := range strings.FieldsFunc(string(reply), func(r rune) bool { return r == lf || r == cr || r == etx }) {
		switch {
		case strings.HasPrefix(f, "S") && len(f) >= 3:
			status = f[1:]
		case f == "?":
			return domain.Reading{}, domain.ErrScaleReply
		default:
			weight = f
		}
	}
	if status == "" {
		return domain.Reading{}, domain.ErrScaleReply
	}
	s1, s2 := status[0], status[1]
	switch {
	case s1&0x0c != 0, s2&0x0c != 0, len(status) > 2 && s2&0x40 != 0 && status[2]&0x08 != 0:
		return domain.Reading{}, domain.ErrScaleFault
	case s2&0x02 != 0:
		return domain.Reading{}, domain.ErrScaleOver
	case s2&0x01 != 0:
		return domain.Reading{}, domain.ErrScaleNegative
	case s1&0x01 != 0:
		return domain.Reading{Stable: false}, nil
	case weight == "":
		// No weight, no problem either: the scale is at zero.
		return domain.Reading{Stable: true}, nil
	}
	var g int64
	var err error
	switch {
	case strings.HasSuffix(weight, "KG"):
		g, err = grams(strings.TrimSuffix(weight, "KG"))
	case strings.HasSuffix(weight, "GM"):
		g, err = strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(weight, "GM")), 10, 64)
		if err != nil {
			err = domain.ErrScaleReply
		}
	default:
		return domain.Reading{}, domain.ErrScaleUnits
	}
	if err != nil {
		return domain.Reading{}, err
	}
	return domain.Reading{Grams: g, Stable: true}, nil
}
