package pubsub

import (
	"bytes"
	"testing"
	"time"
)

func TestSubscribers(t *testing.T) {
	tests := []struct {
		name string
		// number of subscriptions to create
		total int
	}{
		{
			name:  "4 subscriptions",
			total: 4,
		},
	}

	p := New(DefaultBufferSize)

	var list []*Subscription

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for i := 0; i < test.total; i++ {
				sub, err := p.Subscribe(test.name)
				if err != nil {
					t.Errorf("unable to subscribe: %s", err)
				}
				list = append(list, sub)
			}
			cnt := p.Subscribers(test.name)
			if cnt != test.total {
				t.Errorf("expected: %d, got %d\n", test.total, cnt)
			}

			for _, sub := range list {
				if err := sub.Unsubscribe(); err != nil {
					t.Errorf("unable to unsubscribe: %s", err)
				}
			}

			cnt = p.Subscribers(test.name)
			if cnt != 0 {
				t.Errorf("expected: 0, got %d\n", cnt)
			}

		})
	}
}

func TestClose(t *testing.T) {
	p := New(DefaultBufferSize)
	t.Run("test close", func(t *testing.T) {
		for i := 0; i < 16; i++ {
			_, err := p.Subscribe("test close")
			if err != nil {
				t.Errorf("unable to subscribe: %s", err)
			}
		}
		p.Close()

		cnt := p.Subscribers("test close")
		if cnt != 0 {
			t.Errorf("expected: 0, got %d\n", cnt)
		}
	})
}

func TestPublish(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{
			name: "simple ping",
			in:   []byte("guava is great"),
		},
	}

	p := New(DefaultBufferSize)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sub, err := p.Subscribe(test.name)
			if err != nil {
				if err != nil {
					t.Errorf("unable to subscribe: %s", err)
				}
			}
			go p.Publish(test.name, test.in)

			select {
			case result := <-sub.Messages:
				if !bytes.Equal(result, test.in) {
					t.Errorf("expected: %s, got: %s", string(test.in), string(result))
				}
			case <-time.After(1 * time.Second):
				t.Error("timed out waiting for message")
			}
		})
	}
}
