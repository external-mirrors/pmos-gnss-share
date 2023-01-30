// Copyright 2022 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	dbus "github.com/godbus/dbus/v5"
	"gitlab.com/postmarketOS/gnss-share/internal/pool"
)

const (
	// MMModemLocationSource
	mmModemLocationSourceGpsNmea uint32 = 1 << 2
	// MMModemLocationAssistanceDataType
	mmModemLocationAssistanceDataTypeXtra uint32 = 1
)

type Qcom struct {
	debug          bool
	readerPool     *pool.Pool
	readerPoolDone <-chan struct{}
}

func NewQcom(debug bool) *Qcom {
	q := Qcom{
		debug:          debug,
		readerPoolDone: make(chan struct{}),
	}
	q.readerPool = pool.New(q.readerPoolDone)

	return &q
}

func (q *Qcom) Start(sendCh chan<- []byte, stop <-chan bool, errCh chan<- error) {
	// TODO: just in case these aren't needed, here are the stubs to uncomment
	// later:
	// open := func() error { return nil }
	// close := func() error { return nil }

	start_poll := func() (<-chan []byte, error) {
		conn, err := dbus.ConnectSystemBus()
		if err != nil {
			return nil, err
		}
		// locCaps := []uint32{mmModemLocationSourceGpsNmea}
		// TODO: use CallWithContext() so this can timeout
		// TODO: get modem, don't hardcode...
		mmObj := conn.Object("org.freedesktop.ModemManager1", dbus.ObjectPath("/org/freedesktop/ModemManager1/Modem/0"))
		err = mmObj.Call("org.freedesktop.ModemManager1.Modem.Location.Setup", 0, mmModemLocationSourceGpsNmea, false).Err
		if err != nil {
			return nil, fmt.Errorf("unable to enable location on modem: %w", err)
		}

		// Determine if XTRA is supported, error if not
		var supported uint32
		if err = mmObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.SupportedAssistanceData", &supported); err != nil {
			return nil, fmt.Errorf("unable to determine supported assistance data: %w", err)
		}
		if (supported & mmModemLocationAssistanceDataTypeXtra) == 0 {
			return nil, fmt.Errorf("XTRA assistance data is not supported")
		}

		// Get a-gps server config from MM
		servers := []string{}
		if err = mmObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.AssistanceDataServers", &servers); err != nil {
			return nil, fmt.Errorf("unable to retrieve list of a-gps servers: %w", err)
		}
		if q.debug {
			fmt.Println("a-gps servers from MM: ", servers)
		}

		// Download a-gps data
		// TODO: don't hardcode selection
		// TODO: make configurable / overrideable
		// TODO: don't fetch/inject a-gps data if done previously(?)
		// TODO: the a-gps data seems to be pretty small, <100KB, but maybe it
		// 	should be written out to a file instead of kept in memory just in
		// 	case?
		resp, err := http.Get(servers[0])
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve a-gps data from %q: %w", servers[0], err)
		}
		defer resp.Body.Close()
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("unable to parse a-gps data from %q: %w", servers[0], err)
		}

		// Inject a-gps data
		err = mmObj.Call("org.freedesktop.ModemManager1.Modem.Location.InjectAssistanceData", 0, data).Err
		if err != nil {
			return nil, fmt.Errorf("unable to inject a-gps data from %q: %w", servers[0], err)
		}

		sendCh := make(chan []byte)

		go func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(1 * time.Second): // TODO: make interval configurable
					ret := make(map[uint32]dbus.Variant)
					if err := mmObj.Call("org.freedesktop.ModemManager1.Modem.Location.GetLocation", 0).Store(&ret); err != nil {
						fmt.Println("ERROR: unable to get location data from response: ", err)
						continue

					}
					if val, ok := ret[mmModemLocationSourceGpsNmea]; !ok {
						fmt.Println("ERROR: unable to find location key '4' in returned data: ", ret)
						continue
					} else {
						if q.debug {
							fmt.Println("received location data from modem: ", val)
						}
						b := []byte{}
						if err := val.Store(&b); err != nil {
							fmt.Println("ERROR: unable parse: ", val.String())
							continue
						}
						sendCh <- b
					}
				}
			}
		}()

		return sendCh, nil
	}

	recvCh, err := start_poll()
	if err != nil {
		errCh <- err
		return
	}

	for {
		select {
		case <-stop:
			// TODO: disable Location in MM
			return
		case line := <-recvCh:
			sendCh <- line
		}
	}
}

func (q *Qcom) Save(dir string) (err error) {
	return
}

func (q *Qcom) Load(dir string) (err error) {
	return
}

// func (q *Qcom) open() error {
// 	return nil
// }

// func (q *Qcom) close() error {
// 	return nil
// }
