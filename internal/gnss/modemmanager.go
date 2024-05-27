// Copyright 2023 Teemu Ikonen <tpikonen@mailbox.org>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"bytes"
	"context"
	"fmt"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

const (
	// Timeout for Dbus calls
	DbusCallTimeout = 10 * time.Second
	// Timeout for GPS setup Dbus calls
	// (MM GPS setup sometimes takes > 20 s, e.g. on Oneplus 6)
	GpsSetupTimeout = 50 * time.Second
	// MMModemLocationSource
	mmModemLocationSourceGpsNmea uint32 = 1 << 2
)

type ModemManager struct {
	modemObj        dbus.BusObject
	systemBus       *dbus.Conn
	refreshInterval uint32
	debug           bool
}

func NewModemManager(debug bool) *ModemManager {
	m := ModemManager{
		modemObj:        nil,
		systemBus:       nil,
		refreshInterval: 1,
		debug:           debug,
	}
	return &m
}

func (m *ModemManager) ensure_bus() error {
	if m.systemBus != nil {
		return nil
	}
	var err error
	m.systemBus, err = dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), DbusCallTimeout)
	err = m.systemBus.AddMatchSignalContext(ctx,
		dbus.WithMatchObjectPath("/org/freedesktop/ModemManager1"),
		dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"),
		dbus.WithMatchMember("InterfacesAdded"),
	)
	cancel() // Free context resources
	if err != nil {
		return err
	}
	ctx, cancel = context.WithTimeout(context.Background(), DbusCallTimeout)
	err = m.systemBus.AddMatchSignalContext(ctx,
		dbus.WithMatchObjectPath("/org/freedesktop/ModemManager1"),
		dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"),
		dbus.WithMatchMember("InterfacesRemoved"),
	)
	cancel() // Free context resources
	if err != nil {
		return err
	}

	return nil
}

func (m *ModemManager) initialize_modem() error {
	if err := m.ensure_bus(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), DbusCallTimeout)
	manager := m.systemBus.Object("org.freedesktop.ModemManager1", dbus.ObjectPath("/org/freedesktop/ModemManager1"))
	getcall := manager.CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0)
	cancel()
	if getcall.Err != nil {
		return fmt.Errorf("unable to enumerate modems")
	}
	// The return value of GetManagedObjects is a somewhat involved map of map of map,
	// see https://dbus.freedesktop.org/doc/dbus-specification.html#standard-interfaces-objectmanager
	// We're only interested in the keys of the first map (the object paths) though.
	modems := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	getcall.Store(&modems)
	if len(modems) < 1 {
		return fmt.Errorf("no modems found")
	}
	paths := make([]dbus.ObjectPath, 0, len(modems))
	for k := range modems {
		paths = append(paths, k)
	}
	if m.debug {
		fmt.Println("Modems found:", paths)
	}

	m.modemObj = nil
	// Find a modemObj which has GPS NMEA capability
	for _, path := range paths {
		var capabilities uint32

		modem := m.systemBus.Object("org.freedesktop.ModemManager1", path)
		err := modem.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.Capabilities", &capabilities)
		if err != nil || (capabilities&mmModemLocationSourceGpsNmea == 0) {
			continue
		}
		if m.debug {
			fmt.Printf("Modem %s has GPS\n", path)
		}
		m.modemObj = modem
		break
	}
	if m.modemObj == nil {
		return fmt.Errorf("could not find modem with GPS capability")
	}
	// Add listener for changed ModemLocation properties
	ctx, cancel = context.WithTimeout(context.Background(), DbusCallTimeout)
	err := m.systemBus.AddMatchSignalContext(ctx,
		dbus.WithMatchObjectPath(m.modemObj.Path()),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, "org.freedesktop.ModemManager1.Modem.Location"),
	)
	cancel() // Free context resources
	if err != nil {
		return err
	}

	return nil
}

func (m *ModemManager) enable_gps() error {
	var enabled uint32 = 0
	err := m.modemObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.Enabled", &enabled)
	if err != nil {
		return fmt.Errorf("unable to get enabled location sources: %w", err)
	}

	if enabled&mmModemLocationSourceGpsNmea == 0 {
		var signals_location bool = false
		err = m.modemObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.SignalsLocation", &signals_location)
		if err != nil {
			return fmt.Errorf("unable to get SignalsLocation property: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), GpsSetupTimeout)
		// Don't downgrade anything: Leave sources other than gps-nmea and SignalsLocation as they were
		err = m.modemObj.CallWithContext(ctx, "org.freedesktop.ModemManager1.Modem.Location.Setup", 0, enabled|mmModemLocationSourceGpsNmea, signals_location).Err
		cancel() // Free context resources
		if err != nil {
			return fmt.Errorf("unable to setup GPS: %w", err)
		}
	}

	var refresh_rate uint32 = 0
	err = m.modemObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.GpsRefreshRate", &refresh_rate)
	if err != nil {
		return fmt.Errorf("unable to get GpsRefreshRate: %w", err)
	}
	// Call SetGpsRefreshRate only if the interval is higher than what we want
	if refresh_rate > m.refreshInterval {
		ctx, cancel := context.WithTimeout(context.Background(), DbusCallTimeout)
		err = m.modemObj.CallWithContext(ctx, "org.freedesktop.ModemManager1.Modem.Location.SetGpsRefreshRate", 0, m.refreshInterval).Err
		cancel() // Free context resources
		if err != nil {
			return fmt.Errorf("unable to set GPS refresh rate: %w", err)
		}
	}

	return nil
}

func (m *ModemManager) finalize_modem() {
	// Remove listener for changed ModemLocation properties
	ctx, cancel := context.WithTimeout(context.Background(), DbusCallTimeout)
	err := m.systemBus.RemoveMatchSignalContext(ctx,
		dbus.WithMatchObjectPath(m.modemObj.Path()),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, "org.freedesktop.ModemManager1.Modem.Location"),
	)
	cancel()
	if err != nil {
		fmt.Println("unable to remove signal listeners:", err)
	}

	// Disable GPS NMEA, but keep other sources as they were
	var enabled uint32 = 0
	err = m.modemObj.StoreProperty("org.freedesktop.ModemManager1.Modem.Location.Enabled", &enabled)
	if err != nil {
		fmt.Println("unable to get enabled location sources:", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), DbusCallTimeout)
	err = m.modemObj.CallWithContext(ctx, "org.freedesktop.ModemManager1.Modem.Location.Setup", 0, enabled&^mmModemLocationSourceGpsNmea, false).Err
	cancel()
	if m.debug && err != nil {
		fmt.Println("Unable to disable GPS location on modem:", err)
	}
}

func (m *ModemManager) handle_signal(signal *dbus.Signal) {
	var enabled uint32 = 0
	var refresh_rate uint32 = 0
	var need_reset bool = false

	if signal.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
		if m.debug {
			fmt.Printf("Received unexpected signal: %s\n", signal.Name)
		}
		return
	}

	changed_props, ok := signal.Body[1].(map[string]dbus.Variant)
	if !ok {
		return
	}

	if val, ok := changed_props["Enabled"]; ok {
		val.Store(&enabled)
		need_reset = (enabled&mmModemLocationSourceGpsNmea == 0) || need_reset
		if m.debug {
			fmt.Println("Enabled changed to:", enabled)
		}
	}
	if val, ok := changed_props["GpsRefreshRate"]; ok {
		val.Store(&refresh_rate)
		need_reset = refresh_rate > m.refreshInterval || need_reset
		if m.debug {
			fmt.Println("GpsRefreshRate changed to:", refresh_rate)
		}
	}

	if need_reset {
		fmt.Println("Re-enabling GPS")
		if err := m.enable_gps(); err != nil {
			fmt.Println("Error enabling GPS:", err)
		}
	}
}

func (m *ModemManager) Start(send func(data []byte), write <-chan []byte, stop <-chan struct{}) error {
	if err := m.ensure_bus(); err != nil {
		return err
	}

	refresh_interval := time.Duration(m.refreshInterval * uint32(time.Second))

	go func() {
		var errcount = 0
		const max_errors = 2

		signalChan := make(chan *dbus.Signal, 1)
		m.systemBus.Signal(signalChan)
		defer m.systemBus.RemoveSignal(signalChan)

	run_loop:
		// Repeat modem initialization and location polling until stopped.
		for {

		init_loop:
			for {
				if err := m.initialize_modem(); err != nil {
					fmt.Println("Error initializing modem:", err)
				} else {
					if err = m.enable_gps(); err != nil {
						fmt.Println("Error enabling GPS:", err)
					} else {
						break init_loop
					}
				}
				select {
				case <-stop:
					return
				case msg := <-write:
					fmt.Println("message received, but mm driver is unable to forward:", string(msg))
				case signal := <-signalChan:
					if m.debug {
						fmt.Println("Signal received from uninitialized modem:", signal.Name)
					}
				case <-time.After(60 * time.Second):
				}
				fmt.Println("Retrying modem init")
			}

			errcount = 0
			stopped := false
			last_loc_time := time.Now().Add(-1 * refresh_interval)
		poll_loop:
			// This loop exits either when stopped, or when we get more than
			// max_errors consecutive errors. In case of errors, initialize again.
			for {
				select {
				case <-stop:
					stopped = true
					break poll_loop
				case msg := <-write:
					fmt.Println("message received, but mm driver is unable to forward:", string(msg))
				case signal := <-signalChan:
					m.handle_signal(signal)
				case <-time.After(time.Until(last_loc_time.Add(refresh_interval))):
					// Leave select
				}
				if time.Since(last_loc_time) > refresh_interval {
					last_loc_time = time.Now()
					loc_ctx, cancel := context.WithTimeout(context.Background(), refresh_interval)
					loc_call := m.modemObj.CallWithContext(loc_ctx, "org.freedesktop.ModemManager1.Modem.Location.GetLocation", 0)
					cancel()
					if loc_call.Err != nil {
						fmt.Println("ERROR: unable to get location data from response: ", loc_call.Err)
						errcount++
						continue
					}
					loc := make(map[uint32]dbus.Variant)
					loc_call.Store(&loc)
					val, ok := loc[mmModemLocationSourceGpsNmea]
					if !ok {
						// MM can return locations without NMEA data, so this is not an error
						continue
					}
					b := []byte{}
					if err := val.Store(&b); err != nil {
						fmt.Println("ERROR: unable to Store NMEAs: ", val.String())
						errcount++
						continue
					}
					// Remove double newlines and add one to the end
					send(append(
						bytes.ReplaceAll(b, []byte("\r\n\r\n"), []byte("\r\n")),
						[]byte("\r\n")...))
					errcount = 0
				}
				if errcount >= max_errors {
					// The modem probably disappeared, so go back to init_loop
					// to find and initialize it.
					if m.debug {
						fmt.Println("Too many errors when requesting location, re-initializing modem")
					}
					break poll_loop
				}
			}
			// Stop modem GPS on both modem error and when stopped
			m.finalize_modem()
			m.modemObj = nil
			// Only exit run_loop (and the goroutine) when stopped.
			if stopped {
				break run_loop
			}
		}
	}()

	return nil
}

func (m *ModemManager) Save(dir string) (err error) {
	return
}

func (m *ModemManager) Load(dir string) (err error) {
	return
}
