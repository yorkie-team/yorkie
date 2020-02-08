package yorkie

import (
	"encoding/json"
	"io/ioutil"
	"os"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
)

type Config struct {
	RPCPort int
	Mongo   *mongo.Config
}

func NewConfig(path string) (*Config, error) {
	conf := &Config{}
	file, err := os.Open(path)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	if err := json.Unmarshal(bytes, conf); err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	return conf, nil
}
