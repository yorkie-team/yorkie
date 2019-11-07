package backend

import (
	"github.com/hackerwins/yorkie/yorkie/backend/mongo"
)

type Backend struct {
	Mongo *mongo.Client
}

func New(conf *mongo.Config) (*Backend, error) {
	client, err := mongo.NewClient(conf)

	if err != nil {
		return nil, err
	}

	return &Backend{
		Mongo: client,
	}, nil
}

func (b *Backend) Close() error {
	if err := b.Mongo.Close(); err != nil {
		return err
	}

	return nil
}
