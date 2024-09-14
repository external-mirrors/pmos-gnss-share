// stripped down version of stm driver to just forward NMEA
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"gitlab.com/postmarketOS/gnss-share/internal/gnss/common"
	"gitlab.com/postmarketOS/gnss-share/internal/pubsub"
)

// name collision with stm driver
//const internalTopic = "nmea_internal"

type NMEADevice struct {
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

func (s *NMEADevice) open() error {
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
		fmt.Println("nmea received a request to start but it seems to already be started...")
		s.mu.Unlock()
		return nil
	}

	s.stopScan = make(chan struct{})
	s.mu.Unlock()
	// End Protected Section

	go s.readlines(s.device, s.stopScan)

	// 3 seconds should be more than enough time for the module to boot.
	if err := s.ready(time.Duration(3 * time.Second)); err != nil {
		// Don't hold a ref if the open failed (so that it can be retried later)
		s.ref.Remove()
		return fmt.Errorf("nmea device is not ready")
	}

	return nil
}

func (s *NMEADevice) close() error {
	s.ref.Remove()

	if s.ref.Count() >= 1 {
		return nil
	}

	if err := s.device.Close(); err != nil {
		return fmt.Errorf("nmea failed to close device: %w", err)
	}
	s.device = nil

	return nil
}

func NewNMEAGnss(path string, debug bool) *NMEADevice {
	s := NMEADevice{
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
			return nil, fmt.Errorf("nmea: failed opening %q: %w", path, err)
		}
		return os.NewFile(uintptr(fd), s.path), nil
	}

	s.ready = func(t time.Duration) error {
		sub, err := s.bus.Subscribe(internalTopic)
		if err != nil {
			return fmt.Errorf("nmea unable to subscribe to internal topics: %w", err)
		}
		defer func() {
			if err := sub.Unsubscribe(); err != nil {
				fmt.Println("nmea unable to unsubscribe to internal topics: ", err)
			}
		}()

		for {
			select {
			case line := <-sub.Messages:
				if bytes.Contains(line, []byte("$GP")) {
					return nil
				}
			case <-time.After(t):
				return fmt.Errorf("gnss/NMEADevice.ready: timed out waiting for device")
			}
		}
	}

	return &s
}

func (s *NMEADevice) readlines(device io.Reader, done <-chan struct{}) {
	// do the actual scan/read in a new goroutine, so that it's not
	// blocking for the parent goroutine
	lines := func() <-chan []byte {
		out := make(chan []byte)
		go func() {
			defer close(out)
			scanner := bufio.NewScanner(device)
			for scanner.Scan() {
				// Note: this forces a new allocation of the line, so it's not race-y
				data := append([]byte{}, scanner.Bytes()...)
				select {
				case out <- append(data, 0x0D, 0x0A):
				case <-done:
					return
				}
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

			if err := s.bus.Publish(internalTopic, line); err != nil {
				fmt.Println("nmea readlines unable to publish data: ", err)
			}
		}
	}
}

func (s *NMEADevice) Start(send func(data []byte), write <-chan []byte, stop <-chan struct{}) error {
	if err := s.open(); err != nil {
		return fmt.Errorf("nmea unable to open device: %w", err)
	}

	sub, err := s.bus.Subscribe(internalTopic)
	if err != nil {
		return fmt.Errorf("nmea unable to start: %w", err)
	}

	go func() {
		defer func() {
			if err := s.close(); err != nil {
				fmt.Println("nmea unable close device:", err)
			}
			if err := sub.Unsubscribe(); err != nil {
				fmt.Println("nmea unable to unsubscribe to internal topics: ", err)
			}
		}()

		for {
			select {
			case <-stop:
				return
			case line := <-sub.Messages:
				send(line)
			case msg := <-write:
				fmt.Println("nmea message received for forwarding, ignoring:", string(msg))
			}
		}
	}()

	return nil
}

func (s *NMEADevice) Save(dir string) (err error) {
	return
}

func (s *NMEADevice) Load(dir string) (err error) {
	return
}
