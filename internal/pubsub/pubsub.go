package pubsub

import (
	"fmt"
	"strings"
	"sync"
)

type Subscriber interface {
	// Subscribe returns a new subscription
	Subscribe() (*Subscription, error)
}

// ErrNoSubscribers is returned when attempting to publish on a topic that has
// no subscribers
type ErrNoSubscribers struct{}

func (e *ErrNoSubscribers) Error() string { return "topic has no subscribers" }

// ErrNotSubscribed is returned when attempting to unsubscribe from a topic
// when not initially subscribed to it.
type ErrNotSubscribed struct{}

func (e *ErrNotSubscribed) Error() string { return "not subscribed to topic" }

type PubSub struct {
	bufferSize int
	mu         sync.RWMutex
	topics     map[string]([]chan []byte)
}

type Subscription struct {
	Topic string
	// Unsubscribe the given subscription channel from the given topic.
	Unsubscribe func() error
	Messages    <-chan []byte
}

const DefaultBufferSize int = 4096

func New(bufferSize int) *PubSub {
	return &PubSub{
		topics:     make(map[string][]chan []byte),
		bufferSize: bufferSize,
	}
}

// Subscribe to the given topic. Topics are not case senstitive.
func (p *PubSub) Subscribe(topic string) (*Subscription, error) {
	topic = strings.ToLower(topic)

	p.mu.Lock()
	defer p.mu.Unlock()

	// buffered
	msgs := make(chan []byte, p.bufferSize)
	p.topics[topic] = append(p.topics[topic], msgs)
	return &Subscription{
		Unsubscribe: func() error { return p.unsubscribe(topic, msgs) },
		Messages:    msgs,
		Topic:       topic,
	}, nil
}

// Subscribers returns the number of subscriptions for the given topic.
func (p *PubSub) Subscribers(topic string) int {
	topic = strings.ToLower(topic)

	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.topics[topic])
}

// Publish the given data to all subscriptions for the given topic.The
// subscription channels are buffered, however this will drop messages to
// subscribers that aren't ready to receive on their subscription channel when
// the channel buffer is full. Topics are not case sensitive.
func (p *PubSub) Publish(topic string, data []byte) error {
	topic = strings.ToLower(topic)

	p.mu.RLock()
	defer p.mu.RUnlock()

	if subs, ok := p.topics[topic]; !ok || len(p.topics[topic]) == 0 {
		return &ErrNoSubscribers{}
	} else {
		for _, sub := range subs {
			d := append([]byte{}, data...)
			select {
			case sub <- d:
			default: // don't block
			}
		}
	}

	return nil
}

// Unsubscribe the given subscription channel from the given topic. Returns
// ErrNotSubscribed if attempting to unsubscribe from a topic when not
// subscribed to it.
func (p *PubSub) unsubscribe(topic string, sub <-chan []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	found := false
	for i, s := range p.topics[topic] {
		subs := p.topics[topic]
		if sub == s {
			// note: this doesn't preserve order, but I don't think it matters
			// in this case
			subs[i] = subs[len(subs)-1]
			p.topics[topic] = subs[:len(subs)-1]
			found = true
			close(s)
			s = nil

			// Keep going, just in case the sub channel is in the list more
			// than once for some reason...
		}
	}

	if !found {
		return &ErrNotSubscribed{}
	}

	return nil
}

// Close will close all subscription channels for all topics
func (p *PubSub) Close() {
	for topic, subs := range p.topics {
		for _, sub := range subs {
			if err := p.unsubscribe(topic, sub); err != nil {
				fmt.Println("unable to unsubscribe from topic: ", err)
			}
		}
	}
}
