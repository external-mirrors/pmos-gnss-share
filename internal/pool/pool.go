// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package pool

import (
	"sync"
)

type Client struct {
	Send chan []byte
}

type Pool struct {
	Register   chan *Client
	Unregister chan *Client
	Clients    map[*Client]bool
	Broadcast  chan []byte
	sync.Mutex
	stopChan <-chan struct{}
}

func New(stopChan <-chan struct{}) (p *Pool) {
	return &Pool{
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Clients:    make(map[*Client]bool),
		Broadcast:  make(chan []byte),
		stopChan:   stopChan,
	}
}

func (p *Pool) Start() {
	for {
		select {
		case c := <-p.Register:
			p.Lock()
			p.Clients[c] = true
			p.Unlock()
		case c := <-p.Unregister:
			p.Lock()
			delete(p.Clients, c)
			p.Unlock()
		case msg := <-p.Broadcast:
			p.Lock()
			for c := range p.Clients {
				// make a copy to send to each client, since Go would normally
				// send the same ref and cause race-y problems
				msg_copy := make([]byte, len(msg))
				copy(msg_copy, msg)
				select {
				case c.Send <- msg_copy:
				default:
					// don't deadlock if a client isn't receiving for some reason
				}
			}
			p.Unlock()
		case <-p.stopChan:
			return
		}
	}
}

func (p *Pool) Count() (count int) {
	p.Lock()
	defer p.Unlock()

	count = len(p.Clients)
	return
}

func (p *Pool) SetBroadcastChan(newChan chan []byte) {
	p.Lock()
	p.Broadcast = newChan
	defer p.Unlock()
}
