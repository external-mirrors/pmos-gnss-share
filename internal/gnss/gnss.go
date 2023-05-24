// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package gnss

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	"gitlab.com/postmarketOS/gnss-share/internal/pubsub"
)

type GnssDevice string

const (
	Stm GnssDevice = "stm"
)

type GnssDriver interface {
	Load(dir string) error
	Save(dir string) error
	Start(send func(data []byte), write <-chan []byte, stop <-chan struct{}) error
}

type Gnss struct {
	driver     GnssDriver
	driverStop chan struct{}
	bus        *pubsub.PubSub
	dataTopic  string
	writeTopic string
	mu         sync.Mutex
}

func New(driver GnssDriver) (*Gnss, error) {
	return &Gnss{
		dataTopic:  uuid.NewString(),
		writeTopic: uuid.NewString(),
		driver:     driver,
		bus:        pubsub.New(pubsub.DefaultBufferSize),
	}, nil
}

func (g *Gnss) Load(dir string) error {
	return g.driver.Load(dir)
}

func (g *Gnss) Save(dir string) error {
	return g.driver.Save(dir)
}

// Implements io.Writer
func (g *Gnss) Write(p []byte) (int, error) {
	if err := g.bus.Publish(g.writeTopic, p); err != nil {
		return 0, err
	}

	return len(p), nil
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

	pub, err := g.bus.Subscribe(g.writeTopic)
	if err != nil {
		return nil, err
	}

	// Start the driver if this is the first subscription
	if g.bus.Subscribers(g.dataTopic) == 1 {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.driverStop = make(chan struct{})
		// This abstracts publishing to the pubsub topic so that drivers don't
		// have to deal with pubsub if they don't want to.
		// This also hooks up the pub messages to the driver.
		err := g.driver.Start(func(data []byte) {
			if err := g.bus.Publish(g.dataTopic, data); err != nil {
				fmt.Printf("error: unable to send data to %q: %s\n", g.dataTopic, data)
			}
		}, pub.Messages, g.driverStop)
		if err != nil {
			// Unsubscribe so this failed attempt is not counted as an active
			// subscription
			if err := defaultUnsub(); err != nil {
				fmt.Println("stm unable to unsubscribe from internal topic: ", err)
			}
			return nil, err
		}
	}

	return sub, nil
}
