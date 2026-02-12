package scylla

import (
	"context"
	"fmt"

	gotime "time"

	"github.com/gocql/gocql"
	"github.com/yorkie-team/yorkie/server/logging"
)

type Session struct {
	config  *Config
	Session *gocql.Session
}

type SlowQueryObserver struct {
	Threshold gotime.Duration
}

func (s *SlowQueryObserver) ObserveQuery(ctx context.Context, q gocql.ObservedQuery) {
	if q.End.Sub(q.Start) >= s.Threshold {
		logging.DefaultLogger().Warnf(
			"slow query detected: duration=%s, query=%s, values=%v",
			q.Statement,
			q.End.Sub(q.Start),
		)
	}
}

func CreateKeySpace(conf *Config) error {
	sysCluster := gocql.NewCluster(conf.Hosts...)
	sysCluster.Keyspace = "system"
	sysCluster.Consistency = gocql.One
	sysCluster.Timeout = gotime.Second * 5
	sysCluster.ProtoVersion = 4
	sysSesseion, err := sysCluster.CreateSession()
	if err != nil {
		return fmt.Errorf("connect to ScyllaDB system keyspace: %w", err)
	}
	defer sysSesseion.Close()
	dropKeySpaceCQL := `DROP KEYSPACE IF EXISTS ` + conf.Keyspace + `;`
	if err := sysSesseion.Query(dropKeySpaceCQL).Exec(); err != nil {
		return fmt.Errorf("drop keyspace %s: %w", conf.Keyspace, err)
	}
	createKeySpaceCQL := `CREATE KEYSPACE IF NOT EXISTS ` + conf.Keyspace + ` WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 3};`
	if err := sysSesseion.Query(createKeySpaceCQL).Exec(); err != nil {
		return fmt.Errorf("create keyspace %s: %w", conf.Keyspace, err)
	}
	if err := sysSesseion.AwaitSchemaAgreement(context.Background()); err != nil {
		return fmt.Errorf("await schema agreement: %w", err)
	}
	return nil
}

func Dial(conf *Config) (*Session, error) {
	err := CreateKeySpace(conf)
	if err != nil {
		return nil, err
	}

	connectTimeout, err := gotime.ParseDuration(conf.ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("parse connect timeout: %w", err)
	}

	queryTimeout, err := gotime.ParseDuration(conf.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("parse query timeout: %w", err)
	}

	cluster := gocql.NewCluster(conf.Hosts...)
	cluster.Port = conf.Port
	cluster.Keyspace = conf.Keyspace
	cluster.Timeout = queryTimeout
	cluster.ConnectTimeout = connectTimeout
	cluster.Consistency = conf.parseConsistency()
	cluster.ProtoVersion = 4

	if conf.Username != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: conf.Username,
			Password: conf.Password,
		}
	}

	// TODO: Can be configured?
	cluster.NumConns = 2
	cluster.DisableInitialHostLookup = true
	cluster.ReconnectInterval = 1 * gotime.Second

	// Slow query monitoring
	if conf.MonitoringEnabled {
		threshold, err := gotime.ParseDuration(conf.MonitoringSlowQueryThreshold)
		if err != nil {
			return nil, fmt.Errorf("parse slow query threshold: %w", err)
		}
		cluster.QueryObserver = &SlowQueryObserver{
			Threshold: threshold,
		}
	}

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("connect to ScyllaDB: %w", err)
	}

	// Ping (lightweight query)
	if err := session.Query(`SELECT now() FROM system.local`).Exec(); err != nil {
		session.Close()
		return nil, fmt.Errorf("ping ScyllaDB: %w", err)
	}
	logging.DefaultLogger().Infof(
		"ScyllaDB connected, hosts=%v, keyspace=%s",
		conf.Hosts,
		conf.Keyspace,
	)

	if err := createTables(context.Background(), session, conf.Keyspace); err != nil {
		session.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	yorkieClient := &Session{
		config:  conf,
		Session: session,
	}
	return yorkieClient, nil
}

func (s *Session) Close() error {
	s.Session.Close()
	return nil
}
