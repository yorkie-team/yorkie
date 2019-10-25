package rottie

import (
	"sync"

	"github.com/hackerwins/rottie/rottie/api"
)

type Rottie struct {
	lock sync.Mutex

	rpcServer *api.RPCServer

	shutdown   bool
	shutdownCh chan struct{}
}

func New() (*Rottie, error) {
	rpcServer, err := api.NewRPCServer()
	if err != nil {
		return nil, err
	}

	return &Rottie{
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

	close(r.shutdownCh)
	return nil
}

func (r *Rottie) ShutdownCh() <-chan struct{} {
	return r.shutdownCh
}
