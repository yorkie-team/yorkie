package scylla

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/gocql/gocql"
)

type Config struct {
	Hosts []string // ["127.0.0.1"]
	Port  int      // 9042

	Keyspace string

	Username string
	Password string

	Consistency string // "LOCAL_QUORUM", "ONE"

	ConnectTimeout string // "5s"
	QueryTimeout   string // "3s"

	MonitoringEnabled            bool
	MonitoringSlowQueryThreshold string // "200ms"
}

func (c *Config) parseConsistency() gocql.Consistency {
	consistency, err := gocql.ParseConsistencyWrapper(c.Consistency)
	if err != nil {
		return gocql.LocalQuorum
	}
	return consistency
}

func randomKeySpace() string {
	letters := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	result := make([]byte, 8)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "yorkie_test"
		}
		result[i] = letters[num.Int64()]
	}
	return string(result)
}

func GetScyllaConfig() *Config {
	return &Config{
		Hosts:                        []string{"127.0.0.1"},
		Port:                         9042,
		Keyspace:                     "yorkie",
		Username:                     "ADMIN",
		Password:                     "PASSWORD123",
		Consistency:                  "ONE",
		ConnectTimeout:               "5s",
		QueryTimeout:                 "3s",
		MonitoringEnabled:            false,
		MonitoringSlowQueryThreshold: "200ms",
	}
}

func (c *Config) ScyllaDBInfo() string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf(
		"scylla://%s/%s",
		strings.Join(c.Hosts, ","),
		c.Keyspace,
	)
}
