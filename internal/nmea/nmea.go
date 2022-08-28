// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package nmea

import (
	"strconv"
	"strings"
)

func New(sentence string, data []string) []byte {
	// max length of NMEA sentence is 82 (according to wikipedia...)
	buf := make([]byte, 0, 82)
	buf = append(buf, '$')
	buf = append(buf, []byte(sentence)...)
	for _, d := range data {
		buf = append(buf, ',')
		buf = append(buf, []byte(d)...)
	}

	if len(data) == 0 {
		// always make sure the type is followed by a comma if there is no data
		buf = append(buf, ',')
	}

	// add checksum
	sum := checksum(buf[1:])
	buf = append(buf, '*')
	buf = append(buf, sum...)
	return buf
}

func checksum(b []byte) []byte {
	var sum uint8
	for i := 0; i < len(b); i++ {
		sum ^= b[i]
	}

	return []byte(strings.ToUpper(strconv.FormatInt(int64(sum), 16)))
}
