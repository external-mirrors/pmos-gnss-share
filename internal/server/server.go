// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"errors"
	"fmt"
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
	s.sock.Close()
}

func (s *Server) Start(subscriber pubsub.Subscriber) (err error) {
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
					conn.Close()
				} else {
					s.clientConnection(conn, sub)
				}
			}()
		}
	}
}

// Routine run for each client connection
func (s *Server) clientConnection(conn net.Conn, sub *pubsub.Subscription) {

	defer func() {
		conn.Close()
		fmt.Println("Client disconnected")
		if err := sub.Unsubscribe(); err != nil {
			fmt.Println("server: connection unsubscribe failed: ", err)
		}
	}()

	fmt.Println("Client connected")

	for {
		// read all data (inluding buffered) from the channel
		// if there's anything to send, send it
		// don't spin in a loop, and don't use time.Sleep, it should block on channel read

		// TODO: this is allocating on *every* iteration... what's a good way
		// to re-use this slice?
		msg, ok := <-sub.Messages
		if !ok {
			fmt.Println("server: unable to read from client subscription channel")
			return
		}
		// This receive loop will slurp up any data that is in the channel
		// buffers
	receive_loop:
		for {
			select {
			case data := <-sub.Messages:
				msg = append(msg, data...)
			default:
				break receive_loop
			}
		}
		if bytes.Equal(msg, []byte{}) {
			continue
		}

		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			if !errors.Is(err, net.ErrClosed) {
				fmt.Println("server: client unable to set socket write deadline: ", err)
			}
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
