// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package pool

import (
	"testing"
	"time"
)

func TestPool(t *testing.T) {
	poolStop := make(chan struct{})
	defer close(poolStop)
	p := New(poolStop)
	go p.Start()

	reader := Client{}

	select {
	case p.Register <- &reader:
		t.Log("registered!")
		break
	case <-time.After(1 * time.Second):
	}

	count := p.Count()
	p.Lock()
	t.Log(p.Clients)
	p.Unlock()
	if count != 1 {
		t.Fatalf("unexpected number of registered clients, got: %d, expected: 1", count)
	}
}
