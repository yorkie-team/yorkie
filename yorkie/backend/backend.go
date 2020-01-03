package backend

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/sync"
	"github.com/hackerwins/yorkie/yorkie/backend/mongo"
	"github.com/hackerwins/yorkie/yorkie/pubsub"
)

type Backend struct {
	Mongo    *mongo.Client
	mutexMap *sync.MutexMap
	pubSub   *pubsub.PubSub
}

func New(conf *mongo.Config) (*Backend, error) {
	client, err := mongo.NewClient(conf)
	if err != nil {
		return nil, err
	}

	return &Backend{
		Mongo:    client,
		mutexMap: sync.NewMutexMap(),
		pubSub:   pubsub.NewPubSub(),
	}, nil
}

func (b *Backend) Close() error {
	if err := b.Mongo.Close(); err != nil {
		return err
	}

	return nil
}

func (b *Backend) Lock(k string) error {
	return b.mutexMap.Lock(k)
}

func (b *Backend) Unlock(k string) error {
	return b.mutexMap.Unlock(k)
}

func (b *Backend) Subscribe(actor *time.ActorID, keys []string) (*pubsub.Subscription, error) {
	return b.pubSub.Subscribe(actor, keys)
}

func (b *Backend) Unsubscribe(keys []string, subscription *pubsub.Subscription) {
	b.pubSub.Unsubscribe(keys, subscription)
}

func (b *Backend) Publish(actor *time.ActorID, key string, event pubsub.Event) {
	b.pubSub.Publish(actor, key, event)
}
