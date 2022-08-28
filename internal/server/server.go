// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"gitlab.com/postmarketOS/gnss-share/internal/pool"
)

type Server struct {
	socket    string
	sockGroup string
	connPool  *pool.Pool
	sock      net.Listener
	startChan chan<- bool
	stopChan  chan<- bool
}

// Create a new Server. The server will send 'true' to startChan when the first
// client connects, and 'true' to stopChan when the last client disconnects.
// Messages received from the connPool are forwarded to the connected clients.
func New(socket string, sockGroup string, startChan chan<- bool, stopChan chan<- bool, connPool *pool.Pool) (s *Server) {
	s = &Server{
		socket:    socket,
		sockGroup: sockGroup,
		startChan: startChan,
		stopChan:  stopChan,
		connPool:  connPool,
	}

	return
}

func (s *Server) Start() (err error) {
	if err := os.RemoveAll(s.socket); err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}

	s.sock, err = net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("startServer(): %w", err)
	}
	defer s.sock.Close()

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

	// connection handler
	fmt.Printf("Starting GNSS server, accepting connections at: %s\n", s.socket)
	go s.connectionHandler()

	s.connectionHandler()
	return nil
}

func (s *Server) connectionHandler() error {
	for {
		conn, err := (s.sock).Accept()
		if err != nil {
			return fmt.Errorf("server.connectionHandler: %w", err)
		}

		client := pool.Client{
			Send: make(chan []byte, 1),
		}

		if s.connPool.Count() == 0 {
			// client is first one in the connPool
			s.startChan <- true
		}

		s.connPool.Register <- &client

		go s.clientConnection(&client, &conn)

		fmt.Println("New client connected")

	}
}

// Routine run for each client connection
func (s *Server) clientConnection(c *pool.Client, conn *net.Conn) {
	defer func() {
		s.connPool.Unregister <- c
		(*conn).Close()
	}()

	var tries int
	maxTries := 5
	for {
		if tries >= maxTries {
			fmt.Println("Unix sock server: unable to send to client, disconnecting.")
			break
		}
		msg := <-c.Send
		(*conn).SetWriteDeadline(time.Now().Add(1 * time.Second))
		if _, err := (*conn).Write(msg); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				fmt.Printf("Unix sock server: timeout trying to send line to client, retrying (%d of %d)...\n", tries+1, maxTries)
				tries++
				continue
			}
			// client disconnected
			break
		}

		// successful send, reset tries counter
		tries = 0
	}

	// client disconnected
	fmt.Println("Client disconnected")
	if s.connPool.Count() == 1 {
		// client is last one in the pool
		fmt.Println("No clients connected, closing GNSS")
		s.stopChan <- true
	}
}
