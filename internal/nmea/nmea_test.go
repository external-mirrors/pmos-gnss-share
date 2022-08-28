// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package nmea

import (
	"bytes"
	"testing"
)

// Test sentence checksumming
func TestChecksum(t *testing.T) {
	tables := []struct {
		in       string
		expected string
	}{
		{"GPGLL,0000.00000,N,00000.00000,E,070254.000,V,N", "45"},
		{"GNGSA,A,1,,,,,,,,,,,,,99.0,99.0,99.0", "1E"},
		{"PSTMDUMPEPHEMS,", "3C"},
	}

	for _, table := range tables {
		out := checksum([]byte(table.in))
		if !bytes.Equal(out, []byte(table.expected)) {
			t.Errorf("%q expected: %q, got: %q", table.in, table.expected, out)
		}
	}
}

// Test sentence stringer
func TestStringer(t *testing.T) {
	tables := []struct {
		inType   string
		inData   []string
		expected string
	}{
		{"PSTMGPSSUSPEND", []string{}, "$PSTMGPSSUSPEND,*38"},
		{"GPGGA", []string{"070319.000", "0000.00000", "N", "00000.00000", "E", "0", "00", "99.0", "100.00", "M", "0.0", "M", "", ""}, "$GPGGA,070319.000,0000.00000,N,00000.00000,E,0,00,99.0,100.00,M,0.0,M,,*60"},
	}

	for _, table := range tables {
		out := New(table.inType, table.inData)

		if !bytes.Equal(out, []byte(table.expected)) {
			t.Errorf("%q, %q expected: %q, got: %q", table.inType, table.inData, table.expected, out)
		}
	}
}
