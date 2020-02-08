package yorkie

import (
	"sync"

	"github.com/yorkie-team/yorkie/yorkie/api"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

type Yorkie struct {
	lock sync.Mutex

	backend   *backend.Backend
	rpcServer *api.RPCServer

	shutdown   bool
	shutdownCh chan struct{}
}

func New(conf *Config) (*Yorkie, error) {
	be, err := backend.New(conf.Mongo)
	if err != nil {
		return nil, err
	}

	rpcServer, err := api.NewRPCServer(conf.RPCPort, be)
	if err != nil {
		return nil, err
	}

	return &Yorkie{
		backend:    be,
		rpcServer:  rpcServer,
		shutdownCh: make(chan struct{}),
	}, nil
}

func (r *Yorkie) Start() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	return r.rpcServer.Start()
}

func (r *Yorkie) Shutdown(graceful bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.shutdown {
		return nil
	}

	if err := r.backend.Close(); err != nil {
		return err
	}

	r.rpcServer.Shutdown(graceful)

	close(r.shutdownCh)
	r.shutdown = true
	return nil
}

func (r *Yorkie) ShutdownCh() <-chan struct{} {
	return r.shutdownCh
}
