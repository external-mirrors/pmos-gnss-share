// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/postmarketOS/gnss-share/internal/config"
	"gitlab.com/postmarketOS/gnss-share/internal/gnss"
	"gitlab.com/postmarketOS/gnss-share/internal/pool"
	"gitlab.com/postmarketOS/gnss-share/internal/server"
)

func usage() {
	flag.CommandLine.Usage()
}

func main() {
	var confFile string
	flag.StringVar(&confFile, "c", "/etc/gnss-share.conf", "Configuration file to use.")
	var help bool
	flag.BoolVar(&help, "h", false, "Print help and quit.")
	var debug bool
	flag.BoolVar(&debug, "d", false, "Enable printing debug output.")

	flag.Usage = func() {
		fmt.Println("usage: gnss-share COMMAND [OPTION...]")
		fmt.Println("Commands:")
		fmt.Printf("  %-12s\t%s\n", "[none]", "The default behavior if no command is specified is to run in \"server\" mode.")
		fmt.Printf("  %-12s\t%s\n", "store", "Store almanac and ephemeris data and quit.")
		fmt.Printf("  %-12s\t%s\n", "load", "Load almanac and ephemeris data and quit.")
		fmt.Println("Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if help {
		usage()
		return
	}

	conf, err := config.Parse(confFile)
	if err != nil {
		log.Fatal(err)
	}

	var driver gnss.GnssDriver

	switch conf.Driver {
	case "stm":
		driver = gnss.NewStmGnss(conf.DevicePath, debug)
	case "stm_serial":
		driver = gnss.NewStmSerial(conf.DevicePath, conf.BaudRate, debug)
	case "qcom":
		driver = gnss.NewQcom(debug)
	}

	switch cmd := flag.Arg(0); cmd {
	case "store":
		err := driver.Save(conf.CachePath)
		if err != nil {
			log.Fatal(err)
		}
		return
	case "load":
		err := driver.Load(conf.CachePath)
		if err != nil {
			log.Fatal(err)
		}
		return
	default:
		if flag.Arg(0) != "" {
			fmt.Printf("Unknown command: %q\n", flag.Arg(0))
			usage()
			return
		}
		// server mode
	}

	quitChan := make(chan struct{})
	defer close(quitChan)

	// connection broadcast pool
	connPool := pool.New(quitChan)
	go connPool.Start()

	// channels for starting/stopping the driver
	stopChan := make(chan bool)
	startChan := make(chan bool)
	errChan := make(chan error)

	go func() {
		for range startChan {
			go driver.Start(connPool.Broadcast, stopChan, errChan)
		}
	}()

	// start signal handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGUSR1:
				fmt.Printf("received SIGUSR1, loading data from %q\n", conf.CachePath)

				if err := driver.Load(conf.CachePath); err != nil {
					// not fatal
					fmt.Printf("error loading data: %s\n", err)
				}
			case syscall.SIGUSR2:
				fmt.Printf("received SIGUSR2, storing data to %q\n", conf.CachePath)

				if err := driver.Save(conf.CachePath); err != nil {
					// not fatal
					fmt.Printf("error storing data: %s\n", err)
				}
			}
		}
	}()

	s := server.New(conf.Socket, conf.OwnerGroup, startChan, stopChan, connPool)

	// Start monitoring for errors
	go func() {
		for {
			select {
			case <-quitChan:
				return
			case err := <-errChan:
				fmt.Println(err)
			}
		}
	}()

	if err := s.Start(); err != nil {
		log.Fatal(err)
	}
}
