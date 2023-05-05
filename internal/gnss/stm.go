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
	"gitlab.com/postmarketOS/gnss-share/internal/nmea"
	"gitlab.com/postmarketOS/gnss-share/internal/pool"
)

type Stm interface {
	open() (err error)
	close() (err error)
	ready() (bool, error)
	Restore() (err error)
	Reset() (err error)
	SetParam(cdbId int, value uint64) (err error)
	GetParam(cdbId int) (val uint64, err error)
}

type StmCommon struct {
	Stm
	path   string
	device io.ReadWriteCloser
	ref    reference
	debug  bool
	sync.Mutex

	stopScan       chan struct{}
	readerPool     *pool.Pool
	readerPoolDone <-chan struct{}

	openDevice func() (io.ReadWriteCloser, error)
}


func NewStmSerial(path string, baud int, debug bool) *StmCommon {
	s := StmCommon{
		path:           path,
		debug:          debug,
		stopScan:       nil,
		readerPoolDone: make(chan struct{}),
	}

	s.openDevice = func() (io.ReadWriteCloser, error) {
		return serial.OpenPort(&serial.Config{
			Name: path,
			Baud: baud,
		})
	}
	s.readerPool = pool.New(s.readerPoolDone)
	s.Stm = &s
	go s.readerPool.Start()
	// TODO: add a stop method for Pool?

	return &s
}

func (s *StmCommon) open() error {
	s.ref.Add()
	if s.ref.Count() > 1 {
		return nil
	}

	var err error
	s.device, err = s.openDevice()
	if err != nil {
		return err
	}

	// Send the command to resume just in case the module is in a suspended
	// state
	s.resume()

	// TODO: 3 seconds should be more than enough time for the module to boot.
	// This is a total hack... but it's simpler than implementing a routine to
	// read data from the module and look for the response that indicates it
	// has booted. Obviously this is far from ideal though...........
	time.Sleep(3 * time.Second)

	s.stopScan = s.startScan(s.device)

	return nil
}

func (s *StmCommon) close() error {
	s.ref.Remove()

	if s.ref.Count() >= 1 {
		return nil
	}

	s.Lock()
	defer s.Unlock()
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

func NewStmGnss(path string, debug bool) *StmCommon {
	s := StmCommon{
		path:           path,
		debug:          debug,
		stopScan:       nil,
		readerPoolDone: make(chan struct{}),
	}
	s.readerPool = pool.New(s.readerPoolDone)
	s.Stm = &s
	go s.readerPool.Start()

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

	return &s
}

func (s *StmCommon) readlines(device io.Reader) (sendCh chan []byte, errCh chan error, stopCh chan struct{}) {
	stopCh = make(chan struct{})
	sendCh = make(chan []byte)
	errCh = make(chan error)

	go func() {
		var line []byte

		defer close(sendCh)
		defer close(errCh)

		scanner := bufio.NewScanner(device)

		for {
			select {
			case <-stopCh:
				return
			default:
				scanner.Scan()
				line = bytes.TrimSpace(scanner.Bytes())
				err := scanner.Err()
				if err != nil {
					// don't block on sending errors
					select {
					case errCh <- err:
					default:
					}
					return
				}

				if len(line) == 0 {
					return
				}

				select {
				case sendCh <- append([]byte{}, line...):
				default:
				}
			}
		}
	}()

	return
}

func (s *StmCommon) startScan(device io.Reader) (stopScan chan struct{}) {
	recvCh, errCh, stopReadlinesCh := s.readlines(device)
	stopScan = make(chan struct{})

	go func() {
		defer close(stopReadlinesCh)

		for {
			select {
			case <-stopScan:
				// TODO:
				return
			case line := <-recvCh:
				// TODO: this could be blocking?
				s.readerPool.Broadcast <- line
			case err := <-errCh:
				if err != nil {
					fmt.Printf("ERROR: startScan: %s\n", err)
				}
			}
		}
	}()

	return
}

func (s *StmCommon) ready() (bool, error) {
	return true, nil
}

// func (s *StmGnss) ready() (bool, error) {
// 	// device sends this message when it has booted
// 	resp := nmea.New("GPTXT", []string{"DEFAULT LIV CONFIGURATION"})

// 	r, err := newReader(s.readerPool, s.open, s.close)
// 	if err != nil {
// 		return false, err
// 	}
// 	defer r.Close()

// 	timeout := time.After(5 * time.Second)
// 	for {
// 		select {
// 		case line := <-r.Read:
// 			if bytes.Contains(line, resp) {
// 				return true, nil
// 			}
// 		case <-timeout:
// 			return false, fmt.Errorf("gnss/StmCommon.ready: timed out waiting for device")
// 		}
// 	}
// }

func (s *StmCommon) Start(sendCh chan<- []byte, stop <-chan bool, errCh chan<- error) {
	// TODO: allow disabling this in config:
	if err := s.setTime(); err != nil {
		errCh <- fmt.Errorf("gnss/stm.Start: %w", err)
		return
	}

	r, err := newReader(s.readerPool, s.open, s.close)
	if err != nil {
		errCh <- err
	}
	defer r.Close()

	for {
		select {
		case <-stop:
			return
		case line := <-r.Read:
			sendCh <- line
		}
	}
}

func (s *StmCommon) Save(dir string) (err error) {
	s.open()
	defer s.close()

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

func (s *StmCommon) Load(dir string) (err error) {
	s.open()
	defer s.close()

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
func (s *StmCommon) GetParam(cdbId int) (val uint64, err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer s.close()

	s.pause()
	defer s.resume()

	// the module responds with STMSETPAR to a GETPAR command
	out, err := s.sendCmd(nmea.New("PSTMGETPAR", []string{fmt.Sprintf("%d", cdbId)}), []byte("SETPAR"))
	if err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	for _, l := range out {
		if strings.Contains(l, "PSTMGETPARERROR") {
			err = fmt.Errorf("gnss/StmCommon.GetParam: PSTMGETPARERROR returned by module")
			return
		}
		if strings.Contains(l, fmt.Sprintf("PSTMSETPAR,%d", cdbId)) {
			msg := strings.Split(l, "*")[0]
			fields := strings.Split(msg, ",")
			if len(fields) < 3 {
				err = fmt.Errorf("gnss/StmCommon.GetParam: not enough fields in response from module")
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
			err = fmt.Errorf("gnss/StmCommon.GetParam: Unable to parse returned value: %q", fields[2])
			return
		}
	}
	err = fmt.Errorf("gnss/StmCommon.GetParam: no response sent by module")
	return
}

// SetParam sets parameters in the given configuration data block. See the STM
// Teseo Liv3f gps software manual sections for PSTMSETPAR and relevant CBD for
// possible IDs/values to use.
func (s *StmCommon) SetParam(cdbId int, value uint64) (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}
	defer s.close()

	s.pause()
	// resume only on error, since system is reset on success

	msgListCmd := nmea.New("PSTMSETPAR", []string{
		fmt.Sprintf("%d%d", 3, cdbId),
		fmt.Sprintf("0x%08x", value),
		// TODO: exposing the OR and AND functionality in the 4th optional
		// parameter to STMSETPAR would be nice
		fmt.Sprintf("%d", 0),
	},
	)
	out, err := s.sendCmd(msgListCmd, []byte("SETPAROK"))
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.SetParam: %w", err)
		s.resume()
		return
	}

	for _, o := range out {
		if strings.Contains(o, "PSTMSETPARERROR") {
			s.resume()
			return fmt.Errorf("error setting parameter at conf block %d, id %d: %d", 1, cdbId, value)
		}
	}

	_, err = s.sendCmd(nmea.New("PSTMSAVEPAR", []string{}), []byte("SAVEPAROK"))
	if err != nil {
		s.resume()
		return
	}
	_, err = s.sendCmd(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmCommon) Reset() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.Reset: %w", err)
		return
	}

	defer s.close()
	s.pause()
	_, err = s.sendCmd(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmCommon) Restore() (err error) {
	if err = s.open(); err != nil {
		err = fmt.Errorf("gnss/stmCommon.GetParam: %w", err)
		return
	}

	defer s.close()
	s.pause()
	// resume only on error, since system is reset on success

	_, err = s.sendCmd(nmea.New("PSTMRESTOREPAR", []string{}), []byte("RESTOREPAROK"))
	if err != nil {
		s.resume()
		return
	}
	_, err = s.sendCmd(nmea.New("PSTMSRR", []string{}), nil)
	return
}

func (s *StmCommon) saveEphemeris(path string) (err error) {
	fmt.Printf("Storing ephemerides to: %q\n", path)

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	out, err := s.sendCmd(nmea.New("PSTMDUMPEPHEMS", []string{}), []byte("DUMPEPHEMS"))
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveEphemeris: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.Save: error saving to file %q: %w", path, err)
		return
	}
	defer fd.Close()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMEPHEM,") {
			fmt.Fprintf(fd, "%s\n", l)
		}
	}
	return
}

func (s *StmCommon) saveAlamanac(path string) (err error) {
	fmt.Printf("Storing almanac to: %q\n", path)

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	out, err := s.sendCmd(nmea.New("PSTMDUMPALMANAC", []string{}), []byte("DUMPALMANAC"))
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveAlmanac: %w", err)
	}

	fd, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.saveAlamanac: error saving to file %q: %w", path, err)
	}
	defer fd.Close()

	for _, l := range out {
		if strings.HasPrefix(l, "$PSTMALMANAC,") {
			fmt.Fprintf(fd, "%s\n", l)
		}
	}
	return
}

// Send the given command to the module. It attempts to get confirmation by
// comparing ackResponse to the data it receives from the module (if
// ackResponse is not empty). An error is returned if ackResponse is not empty
// and it fails to find a match before it times out.
func (s *StmCommon) sendCmd(cmd []byte, ackResponse []byte) (out []string, err error) {
	r, err := newReader(s.readerPool, s.open, s.close)
	if err != nil {
		return out, err
	}
	defer r.Close()
	cmd = bytes.TrimSpace(cmd)
	if s.debug {
		fmt.Println("sendCmd: writing: ", string(cmd))
	}
	err = s.write([]byte(cmd))
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.sendCmd: %w", err)
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
		case line := <-r.Read:
			if s.debug {
				fmt.Printf("read: %s\n", line)
			}
			// Command it echo'd back when it is complete.
			if bytes.Contains(line, ackResponse) {
				break timeout_loop
			}
			out = append(out, string(line))
		case <-timeout:
			return out, fmt.Errorf("gnss/StmCommon.sendCmd: timed out waiting for device")
		}
	}

	return
}

func (s *StmCommon) pause() (err error) {
	_, err = s.sendCmd(nmea.New("PSTMGPSSUSPEND", []string{}), nil)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.pause: %w", err)
	}

	return
}

func (s *StmCommon) resume() (err error) {
	_, err = s.sendCmd(nmea.New("PSTMGPSRESTART", []string{}), nil)
	if err != nil {
		return fmt.Errorf("gnss/StmCommon.pause: %w", err)
	}

	return
}

func (s *StmCommon) write(data []byte) (err error) {
	if s.debug {
		fmt.Printf("write: %s\n", string(data))
	}
	// add crlf
	_, err = s.device.Write(append(data, 0x0D, 0x0A))
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.write: %w", err)
		return
	}

	return
}

func (s *StmCommon) loadEphemeris(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.loadEphemeris: %w", err)
		return
	}
	defer fd.Close()

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	var lines [][]byte
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		lines = append(lines, append([]byte(nil), line...))
	}

	for _, line := range lines {
		_, err = s.sendCmd(line, []byte("EPHEMOK"))
		if err != nil {
			return fmt.Errorf("gnss/StmCommon.loadEphemeris: %w", err)
		}
	}

	return
}

func (s *StmCommon) loadAlmanac(path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		err = fmt.Errorf("gnss/StmCommon.loadAlmanac: %w", err)
		return
	}
	defer fd.Close()

	err = s.pause()
	if err != nil {
		return
	}
	defer s.resume()

	var lines [][]byte

	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		lines = append(lines, append([]byte(nil), line...))
	}

	for _, line := range lines {
		_, err = s.sendCmd(line, []byte("ALMANACOK"))
		if err != nil {
			return fmt.Errorf("gnss/StmCommon.loadAlmanac: %w", err)
		}
	}

	return
}

// Set the time for the module if it deviates from host/OS time.
func (s *StmCommon) setTime() (err error) {
	fmt.Println("Checking date/time on module...")
	var rawTime, rawDate string

	r, err := newReader(s.readerPool, s.open, s.close)
	if err != nil {
		return
	}
	defer r.Close()
	timeout := time.After(5 * time.Second)

get_time_loop:
	for {
		select {
		case line := <-r.Read:
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
		err = fmt.Errorf("stm/setTime: %w", err)
		return
	}

	// Only update date/time on module if it's more than 30 seconds off
	now := time.Now().UTC()
	diff := now.Sub(moduleTime)
	if math.Abs(diff.Seconds()) < 30.0 {
		return
	}

	fmt.Println("Module date/time is incorrect, setting to host date/time...")

	_, err = s.sendCmd(nmea.New("PSTMINITTIME", []string{fmt.Sprintf("%02d,%02d,%04d,%02d,%02d,%02d",
		now.Day(),
		now.Month(),
		now.Year(),
		now.Hour(),
		now.Minute(),
		now.Second(),
	)}), []byte("INITTIME"))
	if err != nil {
		err = fmt.Errorf("stm/setTime: %w", err)
		return
	}

	return
}
