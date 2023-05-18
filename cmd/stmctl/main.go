// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"strconv"

	"gitlab.com/postmarketOS/gnss-share/internal/gnss"
)

func usage() {
	flag.CommandLine.Usage()
}

func main() {
	var devPath string
	flag.StringVar(&devPath, "d", "/dev/gnss0", "Path to STM device")
	var baud int
	flag.IntVar(&baud, "b", 9600, "Baud rate, only applicable if STM device is a serial device *not* using the Linux GNSS subsystem.")
	var serial bool
	flag.BoolVar(&serial, "s", false, "STM device is a serial device (e.g. /dev/tty*) *not* using the Linux GNSS subsystem")
	var debug bool
	flag.BoolVar(&debug, "debug", false, "Enable printing debug output.")

	var help bool
	flag.BoolVar(&help, "h", false, "Print help and quit.")

	flag.Usage = func() {
		fmt.Println("usage: stmctl [OPTION...] COMMAND ")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("Commands:")
		fmt.Printf("  %-12s\t%s\n", "get <CDB-ID>", "Get CDB-ID value.")
		fmt.Printf("  %-12s\t%s\n", "set <CDB-ID> <value>", "Set CDB-ID to given value.")
		fmt.Printf("  %-12s\t%s\n", "restore", "Restore module config to factory defaults.")
		fmt.Printf("  %-12s\t%s\n", "reset", "Reset the module.")
	}

	flag.Parse()

	if help {
		usage()
		return
	}

	var stm *gnss.StmDevice
	if serial {
		stm = gnss.NewStmSerial(devPath, baud, debug)
	} else {
		stm = gnss.NewStmGnss(devPath, debug)
	}

	switch cmd := flag.Arg(0); cmd {
	case "restore":
		stm.Restore()
		return
	case "reset":
		stm.Reset()
		return
	case "set":
		if len(flag.Args()) < 2 {
			usage()
			return
		}
		cdb, err := strconv.ParseInt(flag.Arg(1), 10, 64)
		if err != nil {
			panic(fmt.Errorf("invalid argument %q: %s", flag.Arg(1), err))
		}
		value, err := strconv.ParseUint(flag.Arg(2), 10, 64)
		if err != nil {
			panic(fmt.Errorf("invalid argument %q: %s", flag.Arg(2), err))
		}
		stm.SetParam(int(cdb), value)
		return
	case "get":
		if len(flag.Args()) < 1 {
			usage()
			return
		}
		cdb, err := strconv.ParseInt(flag.Arg(1), 10, 64)
		if err != nil {
			panic(fmt.Errorf("invalid argument %q: %s", flag.Arg(1), err))
		}
		val, err := stm.GetParam(int(cdb))
		if err != nil {
			panic(fmt.Errorf("unable to get CDB ID \"%d\": %s", int(cdb), err))
		}
		fmt.Printf("%d: 0x%02X\n", cdb, val)
	default:
		usage()
		return
	}
}
