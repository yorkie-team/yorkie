package rottie

import (
	"sync"

	"github.com/hackerwins/rottie/rottie/api"
	"github.com/hackerwins/rottie/rottie/backend"
)

type Rottie struct {
	lock sync.Mutex

	backend   *backend.Backend
	rpcServer *api.RPCServer

	shutdown   bool
	shutdownCh chan struct{}
}

func New(conf *Config) (*Rottie, error) {
	be, err := backend.New(conf.Mongo)
	if err != nil {
		return nil, err
	}

	rpcServer, err := api.NewRPCServer(conf.RPCPort, be)
	if err != nil {
		return nil, err
	}

	return &Rottie{
		backend:    be,
		rpcServer:  rpcServer,
		shutdownCh: make(chan struct{}),
	}, nil
}

func (r *Rottie) Start() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	return r.rpcServer.Start()
}

func (r *Rottie) Shutdown(graceful bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if err := r.backend.Close(); err != nil {
		return err
	}

	r.rpcServer.Shutdown(graceful)

	close(r.shutdownCh)
	return nil
}

func (r *Rottie) ShutdownCh() <-chan struct{} {
	return r.shutdownCh
}
