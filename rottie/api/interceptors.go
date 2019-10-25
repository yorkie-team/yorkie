package api

import (
	"context"

	"google.golang.org/grpc"

	"github.com/hackerwins/rottie/pkg/log"
)

func unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	resp, err := handler(ctx, req)
	if err == nil {
		log.Logger.Infof("unary %q(%q) => ok", info.FullMethod, req)
	} else {
		log.Logger.Infof("unary %q(%q) => %q", info.FullMethod, req, err)
	}

	return resp, err
}

func streamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	if err == nil {
		log.Logger.Infof("stream %q => ok", info.FullMethod)
	} else {
		log.Logger.Infof("stream %q => %s", info.FullMethod, err.Error())
	}

	return err
}
