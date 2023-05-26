// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"gitlab.com/postmarketOS/gnss-share/internal/pubsub"
)

type Server struct {
	sock      net.Listener
	socket    string
	sockGroup string
}

// New initializes a new Server. socket is the path to the unix socket that the
// server will create and listen on, and sockGroup is the group owner for the
// socket.
func New(socket string, sockGroup string) (s *Server) {
	s = &Server{
		socket:    socket,
		sockGroup: sockGroup,
	}

	return
}

func (s *Server) Stop() {
	// #nosec G104 - ignore socket close failures on purpose
	s.sock.Close()
}

func (s *Server) Start(subscriber pubsub.Subscriber, publisher io.Writer) (err error) {
	if err := os.RemoveAll(s.socket); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	s.sock, err = net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	defer func() {
		cerr := s.sock.Close()
		if err == nil {
			err = cerr
		}
	}()

	// #nosec G302 - group needs R/W access
	if err := os.Chmod(s.socket, 0660); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	group, err := user.LookupGroup(s.sockGroup)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	gid, err := strconv.ParseInt(group.Gid, 10, 16)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	if err := os.Chown(s.socket, -1, int(gid)); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	fmt.Println("Starting GNSS server, accepting connections at: ", s.socket)

	for {
		if conn, err := (s.sock).Accept(); err != nil {
			fmt.Println("server: connection accept failed: ", err)
			continue
		} else {
			go func() {
				if sub, err := subscriber.Subscribe(); err != nil {
					fmt.Println("server: unable to subscribe to topic: ", err)
					// #nosec G104 - ignore conn close failures on purpose
					conn.Close()
				} else {
					s.clientConnection(conn, sub, publisher)
				}
			}()
		}
	}
}

// Routine run for each client connection
func (s *Server) clientConnection(conn net.Conn, sub *pubsub.Subscription, pub io.Writer) {

	defer func() {
		// Note: close() could potentially be called twice, e.g. once on socket
		// read failure and again here, however upon inspection of the Go
		// implementation for net.Conn.UnixConn this doesn't seem to matter...
		// #nosec G104 - ignore conn close failures on purpose
		conn.Close()
		fmt.Println("Client disconnected")
		if err := sub.Unsubscribe(); err != nil {
			fmt.Println("server: connection unsubscribe failed: ", err)
		}
	}()

	fmt.Println("Client connected")

	// This "read" goroutine conveniently serves as a trigger for when the client
	// closes the connection. Without it, the "write" for loop later will hang
	// indefinitely if no data is sent to the subscription channel. The "done"
	// chan is closed when this "read" goroutine returns, which will happen if
	// the client disconnects.
	done := make(chan struct{})
	go func() {
		// #nosec G307 - ignore conn close failures on purpose
		defer conn.Close()
		defer close(done)

		for {
			buf := make([]byte, 1024)
			size, err := conn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					fmt.Println("server: unable to read from socket: ", err)
				}
				return
			}
			if size == 0 {
				continue
			}
			if _, err := pub.Write(buf); err != nil {
				fmt.Println("server: unable to send received data to sub: ", err)
			}
		}
	}()

	for {
		// read all data (inluding buffered) from the channel
		// if there's anything to send, send it
		// don't spin in a loop, and don't use time.Sleep, it should block on channel read

		var msg []byte
		select {
		case <-done:
			return
		case data, ok := <-sub.Messages:
			if !ok {
				fmt.Println("server: unable to read from client subscription channel")
				return
			}
			msg = data
		}
		// This receive loop will slurp up any data that is in the channel
		// buffers
	receive_loop:
		for {
			select {
			case data := <-sub.Messages:
				msg = append(msg, data...)
			case <-done:
				return
			default:
				break receive_loop
			}
		}
		if bytes.Equal(msg, []byte{}) {
			continue
		}

		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			// Note: this is only supported in Go 1.16... uncomment when the
			// min version is increased to at least that version::
			// if !errors.Is(err, net.ErrClosed) {
			// 	fmt.Println("server: client unable to set socket write deadline: ", err)
			// }
			return
		}

		if _, err := conn.Write(msg); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// Client hasn't read in a long time, so just bail for now. Maybe
				// later this could retry a few times or wait longer?
				fmt.Println("server: client hasn't read from socket in 5s, disconnecting it")
				return
			}
			return
		}
	}
}
