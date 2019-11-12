package api

import (
	"context"
	"time"

	"google.golang.org/grpc"

	"github.com/hackerwins/yorkie/pkg/log"
)

func unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	if err == nil {
		log.Logger.Infof("RPC : %q %s: %q => %q", info.FullMethod, time.Since(start), req, resp)
	} else {
		log.Logger.Infof("RPC : %q %s) %q => %q", info.FullMethod, time.Since(start), req, err)
	}

	return resp, err
}

func streamInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	err := handler(srv, ss)
	if err == nil {
		log.Logger.Infof("stream %q => ok", info.FullMethod)
	} else {
		log.Logger.Infof(
			"stream %q => %s", info.FullMethod, err.Error(),
		)
	}

	return err
}
