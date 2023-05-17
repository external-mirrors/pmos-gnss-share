// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package common

import (
	"gitlab.com/postmarketOS/gnss-share/internal/pool"
	"sync"
)

type reader struct {
	Read chan []byte

	poolClient *pool.Client
	pool       *pool.Pool

	// called when reader is closed
	close func() error
}

// Initializes a new reader and registers it with the given pool, and calls the
// open() func. Any error from open() is bubbled up.
func NewReader(p *pool.Pool, open func() error, close func() error) (*reader, error) {
	if err := open(); err != nil {
		return nil, err
	}

	r := make(chan []byte, 1)
	poolClient := &pool.Client{Send: r}
	p.Register <- poolClient
	return &reader{
		Read:       r,
		poolClient: poolClient,
		close:      close,
		pool:       p,
	}, nil
}

// Unregisters the reader from the Pool and calls the close() func
func (r *reader) Close() error {
	r.pool.Unregister <- r.poolClient
	return r.close()
}

// A simple ref counting type
type Reference struct {
	sync.Mutex
	count int
}

func (r *Reference) Add() {
	r.Lock()
	defer r.Unlock()

	r.count += 1
}

func (r *Reference) Remove() {
	r.Lock()
	defer r.Unlock()

	if r.count < 1 {
		return
	}

	r.count -= 1
}

func (r *Reference) Count() int {
	r.Lock()
	defer r.Unlock()

	return r.count
}
