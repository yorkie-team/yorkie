package pubsub

import (
	"sync"

	"github.com/google/uuid"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type Event struct {
	Type  string
	Value string
}

type Subscription struct {
	id     string
	actor  *time.ActorID
	events chan Event
}

func (s Subscription) Events() <-chan Event {
	return s.events
}

func newSubscription(actor *time.ActorID) *Subscription {
	return &Subscription{
		id:     uuid.New().String(),
		actor:  actor,
		events: make(chan Event),
	}
}

type Subscriptions map[string]*Subscription

// TODO: Temporary PubSub.
//  - We will need to replace this with distributed pubSub.
type PubSub struct {
	mu               *sync.RWMutex
	subscriptionsMap map[string]Subscriptions
}

func NewPubSub() *PubSub {
	return &PubSub{
		mu:               &sync.RWMutex{},
		subscriptionsMap: make(map[string]Subscriptions),
	}
}

func (m *PubSub) Subscribe(
	actor *time.ActorID,
	keys []string,
) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subscription := newSubscription(actor)

	for _, key := range keys {
		if _, ok := m.subscriptionsMap[key]; !ok {
			m.subscriptionsMap[key] = make(Subscriptions)
		}
		m.subscriptionsMap[key][subscription.id] = subscription
	}

	return subscription, nil
}

func (m *PubSub) Unsubscribe(keys []string, subscription *Subscription) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		if subscriptions, ok := m.subscriptionsMap[key]; ok {
			delete(subscriptions, subscription.id)
		}
	}
}

func (m *PubSub) Publish(actor *time.ActorID, key string, event Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if subscriptions, ok := m.subscriptionsMap[key]; ok {
		for _, subscription := range subscriptions {
			if subscription.actor.Compare(actor) != 0 {
				subscription.events <- event
			}
		}
	}
}
