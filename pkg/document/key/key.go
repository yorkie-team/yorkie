package key

import (
	"errors"
	"strings"
)

const (
	BSONSplitter = "$"
)

type Key struct {
	Collection string
	Document   string
}

func FromBSONKey(bsonKey string) (*Key, error) {
	splits := strings.Split(bsonKey, BSONSplitter)
	if len(splits) != 2 {
		return nil, errors.New("fail to create key from bson key")
	}

	return &Key{Collection: splits[0], Document: splits[1]}, nil
}

func (k *Key) BSONKey() string {
	return k.Collection + BSONSplitter + k.Document
}
