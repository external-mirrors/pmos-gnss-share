// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tarm/serial"
	"gitlab.com/postmarketOS/gnss-share/internal/gnss/common"
	"gitlab.com/postmarketOS/gnss-share/internal/nmea"
	"gitlab.com/postmarketOS/gnss-share/internal/pubsub"
)

// Internal pubsub topic, could be made dynamic later if necessary...
const internalTopic = "stm_internal"

type StmDevice struct {
	device     io.ReadWriteCloser
	stopScan   chan struct{}
	openDevice func() (io.ReadWriteCloser, error)
	ready      func(time.Duration) error
	bus        *pubsub.PubSub
	path       string
	ref        common.Reference
	mu         sync.Mutex
	debug      bool
}

func NewStmSerial(path string, baud int, debug bool) *StmDevice {
	s := StmDevice{
		bus:      pubsub.New(pubsub.DefaultBufferSize),
		debug:    debug,
		path:     path,
		stopScan: nil,
	}

	s.openDevice = func() (io.ReadWriteCloser, error) {
		return serial.OpenPort(&serial.Config{
			Name: path,
			Baud: baud,
		})
	}
	s.ready = func(t time.Duration) error { return nil }

	return &s
}

func (s *StmDevice) open() error {
	s.ref.Add()
	if s.ref.Count() > 1 {
		return nil
	}

	// Protected Section
	s.mu.Lock()
	var err error
	s.device, err = s.openDevice()
	if err != nil {
		s.mu.Unlock()
		// Don't hold a ref if the open failed (so that it can be retried later)
		s.ref.Remove()
		return err
	}

	if s.stopScan != nil {
		// shouldn't get here... but just in case...
		fmt.Println("stm received a request to start but it seems to already be started...")
		s.mu.Unlock()
		return nil
	}

	s.stopScan = make(chan struct{})
	go s.readlines(s.device, s.stopScan)

	s.mu.Unlock()
	// End Protected Section

	// Send the command to resume just in case the module is in a suspended
	// state
	if err := s.resume(); err != nil {
		return err
	}

	// TODO: 3 seconds should be more than enough time for the module to boot.
	// This is a total hack... but it's simpler than implementing a routine to
	// read data from the module and look for the response that indicates it
	// has booted. Obviously this is far from ideal though...........
	if err := s.ready(time.Duration(3 * time.Second)); err != nil {
		// Don't hold a ref if the open failed (so that it can be retried later)
		s.ref.Remove()
		return fmt.Errorf("stm device is not ready")
	}

	return nil
}

func (s *StmDevice) close() error {
	s.ref.Remove()

	if s.ref.Count() >= 1 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopScan != nil {
		close(s.stopScan)
		s.stopScan = nil
	}

	if err := s.device.Close(); err != nil {
		return fmt.Errorf("stm failed to close device: %w", err)
	}
	s.device = nil

	return nil
}

func NewStmGnss(path string, debug bool) *StmDevice {
	s := StmDevice{
		bus:      pubsub.New(pubsub.DefaultBufferSize),
		debug:    debug,
		path:     path,
		stopScan: nil,
	}

	s.openDevice = func() (io.ReadWriteCloser, error) {
		// Using syscall.Open will open the file in non-pollable mode, which
		// results in a significant reduction in CPU usage on ARM64 systems,
		// and no noticeable impact on x86_64. We don't need to poll the file
		// since it's just a constant stream of new data from the kernel's GNSS
		// subsystem
		fd, err := syscall.Open(s.path, os.O_RDWR, 0666)
		if err != nil {
			return nil, fmt.Errorf("stm: failed opening %q: %w", path, err)
		}
		return os.NewFile(uintptr(fd), s.path), nil
	}

	s.ready = func(t time.Duration) error {
		// Send the command to resume just in case the module is in a suspended
		// state
		if s.debug {
			fmt.Println("stm.ready: sending resume")
		}
		// send resume in case the device is suspended
		if err := s.resume(); err != nil {
			return err
		}

		// device sends this message when it has booted
		resp := nmea.New("GPTXT", []string{"DEFAULT LIV CONFIGURATION"})

		sub, err := s.bus.Subscribe(internalTopic)
		if err != nil {
			return fmt.Errorf("stm unable to subscribe to internal topics: %w", err)
		}
		defer func() {
			if err := sub.Unsubscribe(); err != nil {
				fmt.Println("stm unable to unsubscribe to internal topics: ", err)
			}
		}()

		for {
			select {
			case line := <-sub.Messages:
				if bytes.Contains(line, resp) {
					return nil
				}
			case <-time.After(t):
				return fmt.Errorf("gnss/StmDevice.ready: timed out waiting for device")
			}
		}
	}

	return &s
}

func (s *StmDevice) readlines(device io.Reader, done <-chan struct{}) {
	// do the actual scan/read in a new goroutine, so that it's not
	// blocking for the parent goroutine
	lines := func() <-chan []byte {
		out := make(chan []byte)
		go func() {
			defer close(out)
			scanner := bufio.NewScanner(device)
			for scanner.Scan() {
				out <- append(scanner.Bytes(), 0x0D, 0x0A)
			}
			if err := scanner.Err(); err != nil {
				// meh
				return
			}
		}()
		return out
	}()

	for {
		select {
		case <-done:
			return
		case line, ok := <-lines:
			if !ok {
				// channel closed, no longer scanning
				return
			}

			if len(line) == 0 {
				continue
			}

			// Note: this forces a new allocation of the line, so it's not race-y
			if err := s.bus.Publish(internalTopic, append([]byte{}, line...)); err != nil {
				fmt.Println("stm readlines unable to publish data: ", err)
			}
		}
	}
}

func (s *StmDevice) Start(send func(data []byte), stop <-chan struct{}) error {
	if err := s.open(); err != nil {
		return fmt.Errorf("stm unable to open device: %w", err)
	}

	sub, err := s.bus.Subscribe(internalTopic)
	if err != nil {
		return fmt.Errorf("stm unable to start: %w", err)
	}

	go func() {
		defer func() {
			if err := s.close(); err != nil {
				fmt.Println("stm unable close device:", err)
			}
			if err := sub.Unsubscribe(); err != nil {
				fmt.Println("stm unable to unsubscribe to internal topics: ", err)
			}
		}()

		// TODO: allow disabling this in config:
		if err := s.setTime(); err != nil {
			fmt.Println("error: stm unable to set time:", err)
			return
		}

		for {
			select {
			case <-stop:
				return
			case line := <-sub.Messages:
				send(line)
			}
		}
	}()

	return nil
}

func (s *StmDevice) Save(dir string) (err error) {
	if err := s.open(); err != nil {
		return fmt.Errorf("stm unable to open device: %w", err)
	}
	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return
	}

	err = s.saveEphemeris(filepath.Join(dir, "ephemeris.txt"))
	if err != nil {
		return
	}

	err = s.saveAlamanac(filepath.Join(dir, "almanac.txt"))
	if err != nil {
		return
	}

	return
}

func (s *StmDevice) Load(dir string) (err error) {
	if err := s.open(); err != nil {
		return fmt.Errorf("stm unable to open device: %w", err)
	}
	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()

	err = s.loadEphemeris(filepath.Join(dir, "ephemeris.txt"))
	if err != nil {
		return
	}

	err = s.loadAlmanac(filepath.Join(dir, "almanac.txt"))
	if err != nil {
		return
	}

	return
}

// GetParam returns the parameter value for the given CDB ID. See the STM Teseo
// Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmDevice) GetParam(cdbId int) (val uint64, err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()

	if err = s.pause(); err != nil {
		return
	}
	defer func() {
		cerr := s.resume()
		if err == nil {
			err = cerr
		}
	}()

	// the module responds with STMSETPAR to a GETPAR command
	out, err := s.write(nmea.New("PSTMGETPAR", []string{fmt.Sprintf("%d", cdbId)}), []byte("SETPAR"))
	if err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	for _, l := range out {
		if strings.Contains(l, "PSTMGETPARERROR") {
			err = fmt.Errorf("gnss/Stm.GetParam: PSTMGETPARERROR returned by module")
			return
		}
		if strings.Contains(l, fmt.Sprintf("PSTMSETPAR,%d", cdbId)) {
			msg := strings.Split(l, "*")[0]
			fields := strings.Split(msg, ",")
			if len(fields) < 3 {
				err = fmt.Errorf("gnss/Stm.GetParam: not enough fields in response from module")
				return
			}
			// try to parse with big.Parse first, sometimes module response is
			// in scientific notation..
			var valBig *big.Float
			valBig, _, err = big.ParseFloat(fields[2], 10, 0, big.ToNearestEven)
			if err == nil {
				val, _ = valBig.Uint64()
				return
			}
			// try parsing with strconv next
			val, err = strconv.ParseUint(fields[2], 0, 64)
			if err == nil {
				return
			}

			// value is in a format that needs to be handled..
			err = fmt.Errorf("gnss/Stm.GetParam: Unable to parse returned value: %q", fields[2])
			return
		}
	}
	err = fmt.Errorf("gnss/Stm.GetParam: no response sent by module")
	return
}

// SetParam sets parameters in the given configuration data block. See the STM
// Teseo Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmDevice) SetParam(cdbId int, value uint64) (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()

	if err = s.pause(); err != nil {
		return
	}
	// resume only on error, since system is reset on success

	msgListCmd := nmea.New("PSTMSETPAR", []string{
		fmt.Sprintf("%d%d", 3, cdbId),
		fmt.Sprintf("0x%08x", value),
		// TODO: exposing the OR and AND functionality in the 4th optional
		// parameter to STMSETPAR would be nice
		fmt.Sprintf("%d", 0),
	},
	)
	out, err := s.write(msgListCmd, []byte("SETPAROK"))
	if err != nil {
		err = fmt.Errorf("gnss/Stm.SetParam: %w", err)
		if err := s.resume(); err != nil {
			fmt.Println("unable to resume: ", err)
		}
		return
	}

	for _, o := range out {
		if strings.Contains(o, "PSTMSETPARERROR") {
			if err := s.resume(); err != nil {
				fmt.Println("unable to resume: ", err)
			}
			return fmt.Errorf("error setting parameter at conf block %d, id %d: %d", 1, cdbId, value)
		}
	}

	_, err = s.write(nmea.New("PSTMSAVEPAR", []string{}), []byte("SAVEPAROK"))
	if err != nil {
		if err := s.resume(); err != nil {
			fmt.Println("unable to resume: ", err)
		}
		return
	}
	_, err = s.write(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmDevice) Reset() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.Reset: %w", err)
		return
	}

	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()
	if err = s.pause(); err != nil {
		return
	}
	_, err = s.write(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmDevice) Restore() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	defer func() {
		cerr := s.close()
		if err == nil {
			err = cerr
		}
	}()

	if err = s.pause(); err != nil {
		return
	}
	// resume only on error, since system is reset on success

	_, err = s.write(nmea.New("PSTMRESTOREPAR", []string{}), []byte("RESTOREPAROK"))
	if err != nil {
		if err := s.resume(); err != nil {
			fmt.Println("unable to resume: ", err)
		}
		return
	}
	_, err = s.write(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmDevice) saveEphemeris(path string) (err error) {
	fmt.Printf("Storing ephemerides to: %q\n", path)

	if err = s.pause(); err != nil {
		return
	}
	defer func() {
		cerr := s.resume()
		if err == nil {
			err = cerr
		}
	}()

	out, err := s.write(nmea.New("PSTMDUMPEPHEMS", []string{}), []byte("DUMPEPHEMS"))
	if err != nil {
		return fmt.Errorf("gnss/Stm.saveEphemeris: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		err = fmt.Errorf("gnss/Stm.Save: error saving to file %q: %w", path, err)
		return
	}

	defer func() {
		cerr := fd.Close()
		if err == nil {
			err = cerr
		}
	}()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMEPHEM,") {
			fmt.Fprintf(fd, "%s", l)
		}
	}
	return
}

func (s *StmDevice) saveAlamanac(path string) (err error) {
	fmt.Printf("Storing almanac to: %q\n", path)

	if err = s.pause(); err != nil {
		return
	}
	defer func() {
		cerr := s.resume()
		if err == nil {
			err = cerr
		}
	}()

	out, err := s.write(nmea.New("PSTMDUMPALMANAC", []string{}), []byte("DUMPALMANAC"))
	if err != nil {
		return fmt.Errorf("gnss/Stm.saveAlmanac: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("gnss/Stm.saveAlamanac: error saving to file %q: %w", path, err)
	}
	defer func() {
		cerr := fd.Close()
		if err == nil {
			err = cerr
		}
	}()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMALMANAC,") {
			fmt.Fprintf(fd, "%s", l)
		}
	}
	return
}

// Send the given command to the module. It attempts to get confirmation by
// comparing ackResponse to the data it receives from the module (if
// ackResponse is not empty). An error is returned if ackResponse is not empty
// and it fails to find a match before it times out.
func (s *StmDevice) write(cmd []byte, ackResponse []byte) (out []string, err error) {
	sub, err := s.bus.Subscribe(internalTopic)
	if err != nil {
		return out, fmt.Errorf("stm unable to subscribe to internal topic: %w", err)
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			fmt.Println("stm unable to unsubscribe to internal topics: ", err)
		}
	}()

	cmd = bytes.TrimSpace(cmd)
	if s.debug {
		fmt.Println("write: ", string(cmd))
	}
	// add crlf
	_, err = s.device.Write(append(cmd, 0x0D, 0x0A))
	if err != nil {
		err = fmt.Errorf("write: %w", err)
		return
	}

	if err != nil {
		err = fmt.Errorf("write: %w", err)
		return
	}

	if len(ackResponse) == 0 {
		return
	}

	// Note: timeout has to be high enough that some commands (e.g. reading
	// ephemeris and almanac) don't time out... Hard-coding this is somewhat
	// hacky... but it's unlikely(?) that a real comand would take more than 30
	// seconds to finish
	timeout := time.After(30 * time.Second)

timeout_loop:
	for {
		select {
		case line := <-sub.Messages:
			if s.debug {
				fmt.Printf("read: %s", line)
			}
			// Command it echo'd back when it is complete.
			if bytes.Contains(line, ackResponse) {
				break timeout_loop
			}
			out = append(out, string(line))
		case <-timeout:
			return out, fmt.Errorf("write: timed out waiting for device")
		}
	}

	return
}

func (s *StmDevice) pause() (err error) {
	_, err = s.write(nmea.New("PSTMGPSSUSPEND", []string{}), nil)
	if err != nil {
		return fmt.Errorf("gnss/Stm.pause: %w", err)
	}

	return
}

func (s *StmDevice) resume() (err error) {
	_, err = s.write(nmea.New("PSTMGPSRESTART", []string{}), nil)
	if err != nil {
		return fmt.Errorf("gnss/Stm.pause: %w", err)
	}

	return
}

func (s *StmDevice) loadEphemeris(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/Stm.loadEphemeris: %w", err)
		return
	}
	defer func() {
		cerr := fd.Close()
		if err == nil {
			err = cerr
		}
	}()

	if err = s.pause(); err != nil {
		return
	}
	defer func() {
		cerr := s.resume()
		if err == nil {
			err = cerr
		}
	}()

	var lines [][]byte
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		lines = append(lines, append([]byte(nil), line...))
	}

	for _, line := range lines {
		_, err = s.write(line, []byte("EPHEMOK"))
		if err != nil {
			return fmt.Errorf("gnss/Stm.loadEphemeris: %w", err)
		}
	}

	return
}

func (s *StmDevice) loadAlmanac(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/Stm.loadAlmanac: %w", err)
		return
	}
	defer func() {
		cerr := fd.Close()
		if err == nil {
			err = cerr
		}
	}()

	if err = s.pause(); err != nil {
		return
	}
	defer func() {
		cerr := s.resume()
		if err == nil {
			err = cerr
		}
	}()

	var lines [][]byte

	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		lines = append(lines, append([]byte(nil), line...))
	}

	for _, line := range lines {
		_, err = s.write(line, []byte("ALMANACOK"))
		if err != nil {
			return fmt.Errorf("gnss/Stm.loadAlmanac: %w", err)
		}
	}

	return
}

// Set the time for the module if it deviates from host/OS time.
func (s *StmDevice) setTime() error {
	fmt.Println("Checking date/time on module...")
	var rawTime, rawDate string

	sub, err := s.bus.Subscribe(internalTopic)
	if err != nil {
		return fmt.Errorf("stm unable to subscribe to internal topic: %w", err)
	}
	defer func() {
		if err := sub.Unsubscribe(); err != nil {
			fmt.Println("stm unable to unsubscribe to internal topics: ", err)
		}
	}()
	timeout := time.After(5 * time.Second)

get_time_loop:
	for {
		select {
		case line := <-sub.Messages:
			// RMC contains both date and time from the module
			if len(line) > 7 && !bytes.Equal(line[:6], []byte("$GPRMC")) {
				continue
			}

			parts := bytes.Split(line, []byte(","))
			if len(parts) < 10 {
				// malformed RMC sentence
				continue
			}

			// $GPRMC,232520.000,A,XXXXXXXX,N,YYYYYYYYYY,W,0.3,326.7,060622,,,A*76
			rawTime = string(parts[1])
			rawDate = string(parts[9])
			break get_time_loop
		case <-timeout:
			return fmt.Errorf("stm/setTime: timed out waiting for device")
		}
	}

	moduleTime, err := time.Parse("020106150405", rawDate+rawTime)
	if err != nil {
		return fmt.Errorf("stm/setTime: %w", err)
	}

	// Only update date/time on module if it's more than 30 seconds off
	now := time.Now().UTC()
	diff := now.Sub(moduleTime)
	if math.Abs(diff.Seconds()) < 30.0 {
		return nil
	}

	fmt.Println("Module date/time is incorrect, setting to host date/time...")

	_, err = s.write(nmea.New("PSTMINITTIME", []string{fmt.Sprintf("%02d,%02d,%04d,%02d,%02d,%02d",
		now.Day(),
		now.Month(),
		now.Year(),
		now.Hour(),
		now.Minute(),
		now.Second(),
	)}), []byte("INITTIME"))
	if err != nil {
		return fmt.Errorf("stm/setTime: %w", err)
	}

	return nil
}
