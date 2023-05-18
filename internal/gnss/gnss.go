// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"fmt"
	"sync"
	"time"

	"gitlab.com/postmarketOS/gnss-share/internal/pubsub"
)

type GnssDevice string

const (
	Stm GnssDevice = "stm"
)

type GnssDriver interface {
	Load(dir string) error
	Save(dir string) error
	Start(send func(data []byte), stop <-chan struct{}) error
}

type Gnss struct {
	dataTopic  string
	driver     GnssDriver
	quit       chan struct{} // TODO: what do to about this quit chan?
	driverStop chan struct{}
	mu         sync.Mutex
	bus        *pubsub.PubSub
}

func New(driver GnssDriver) (*Gnss, error) {
	return &Gnss{
		// Use the current time as a topic, basically minimal minimal effort to
		// avoid collisions, which are likely to be rare anyways.
		dataTopic: fmt.Sprint(time.Now()),
		driver:    driver,
		quit:      make(chan struct{}),
		bus:       pubsub.New(pubsub.DefaultBufferSize),
	}, nil
}

func (g *Gnss) Load(dir string) error {
	return g.driver.Load(dir)
}

func (g *Gnss) Save(dir string) error {
	return g.driver.Save(dir)
}

func (g *Gnss) Subscribe() (*pubsub.Subscription, error) {
	sub, err := g.bus.Subscribe(g.dataTopic)
	if err != nil {
		return nil, err
	}

	// Overload Unsubscribe() to also stop the driver if there are no longer
	// any active subscriptions
	defaultUnsub := sub.Unsubscribe
	sub.Unsubscribe = func() error {
		if err := defaultUnsub(); err != nil {
			return err
		}
		// Stop driver if there are no active subscriptions for our topic
		if g.bus.Subscribers(g.dataTopic) == 0 {
			fmt.Println("Stopping device")
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.driverStop != nil {
				close(g.driverStop)
			}
			g.driverStop = nil
		}
		return nil
	}

	// Start the driver if this is the first subscription
	if g.bus.Subscribers(g.dataTopic) == 1 {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.driverStop = make(chan struct{})
		// This abstracts publishing to the pubsub topic so that drivers don't
		// have to deal with pubsub if they don't want to.
		err := g.driver.Start(func(data []byte) {
			if err := g.bus.Publish(g.dataTopic, data); err != nil {
				fmt.Printf("error: unable to send data to %q: %s\n", g.dataTopic, data)
			}
		}, g.driverStop)
		if err != nil {
			return nil, err
		}
	}

	return sub, nil
}
