/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package admin

import (
	"context"
	"errors"
	"fmt"
	"net"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/admin/interceptors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/grpchelper"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
)

// ErrInvalidAdminPort occurs when the port in the config is invalid.
var ErrInvalidAdminPort = errors.New("invalid port number for Admin server")

// Config is the configuration for creating a Server.
type Config struct {
	Port int `yaml:"Port"`
}

// Validate validates the port number.
func (c *Config) Validate() error {
	if c.Port < 1 || 65535 < c.Port {
		return fmt.Errorf("must be between 1 and 65535, given %d: %w", c.Port, ErrInvalidAdminPort)
	}

	return nil
}

// Server is the gRPC server for admin service.
type Server struct {
	conf       *Config
	grpcServer *grpc.Server
	backend    *backend.Backend
}

// NewServer creates a new Server.
func NewServer(conf *Config, be *backend.Backend) *Server {
	loggingInterceptor := grpchelper.NewLoggingInterceptor()
	defaultInterceptor := interceptors.NewDefaultInterceptor()

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcmiddleware.ChainUnaryServer(
			loggingInterceptor.Unary(),
			be.Metrics.ServerMetrics().UnaryServerInterceptor(),
			defaultInterceptor.Unary(),
		)),
		grpc.StreamInterceptor(grpcmiddleware.ChainStreamServer(
			loggingInterceptor.Stream(),
			be.Metrics.ServerMetrics().StreamServerInterceptor(),
			defaultInterceptor.Stream(),
		)),
	}

	grpcServer := grpc.NewServer(opts...)

	server := &Server{
		conf:       conf,
		backend:    be,
		grpcServer: grpcServer,
	}

	api.RegisterAdminServer(grpcServer, server)
	// TODO(hackerwins): ClusterServer need to be handled by different authentication mechanism.
	// Consider extracting the servers to another grpcServer.
	api.RegisterClusterServer(grpcServer, newClusterServer(be))

	return server
}

// Start starts this server by opening the rpc port.
func (s *Server) Start() error {
	return s.listenAndServeGRPC()
}

// Shutdown shuts down this server.
func (s *Server) Shutdown(graceful bool) {
	if graceful {
		s.grpcServer.GracefulStop()
	} else {
		s.grpcServer.Stop()
	}
}

// GRPCServer returns the gRPC server.
func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

func (s *Server) listenAndServeGRPC() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.conf.Port))
	if err != nil {
		logging.DefaultLogger().Error(err)
		return err
	}

	go func() {
		logging.DefaultLogger().Infof("serving admin on %d", s.conf.Port)

		if err := s.grpcServer.Serve(lis); err != nil {
			if err != grpc.ErrServerStopped {
				logging.DefaultLogger().Error(err)
			}
		}
	}()

	return nil
}

// CreateProject creates a new project.
func (s *Server) CreateProject(
	ctx context.Context,
	req *api.CreateProjectRequest,
) (*api.CreateProjectResponse, error) {
	project, err := projects.CreateProject(ctx, s.backend, req.Name)
	if err != nil {
		return nil, err
	}

	pbProject, err := converter.ToProject(project)
	if err != nil {
		return nil, err
	}
	return &api.CreateProjectResponse{
		Project: pbProject,
	}, nil
}

// ListProjects lists all projects.
func (s *Server) ListProjects(
	ctx context.Context,
	req *api.ListProjectsRequest,
) (*api.ListProjectsResponse, error) {
	projectList, err := projects.ListProjects(ctx, s.backend)
	if err != nil {
		return nil, err
	}

	pbProjects, err := converter.ToProjects(projectList)
	if err != nil {
		return nil, err
	}

	return &api.ListProjectsResponse{
		Projects: pbProjects,
	}, nil
}

// GetProject gets a project.
func (s *Server) GetProject(
	ctx context.Context,
	req *api.GetProjectRequest,
) (*api.GetProjectResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.Name)
	if err != nil {
		return nil, err
	}

	pbProject, err := converter.ToProject(project)
	if err != nil {
		return nil, err
	}
	return &api.GetProjectResponse{
		Project: pbProject,
	}, nil
}

// UpdateProject updates the project.
func (s *Server) UpdateProject(
	ctx context.Context,
	req *api.UpdateProjectRequest,
) (*api.UpdateProjectResponse, error) {
	fields, err := converter.FromUpdatableProjectFields(req.Fields)
	if err != nil {
		return nil, err
	}
	if err = fields.Validate(); err != nil {
		return nil, err
	}

	project, err := projects.UpdateProject(
		ctx,
		s.backend,
		types.ID(req.Id),
		fields,
	)
	if err != nil {
		return nil, err
	}

	pbProject, err := converter.ToProject(project)
	if err != nil {
		return nil, err
	}

	return &api.UpdateProjectResponse{
		Project: pbProject,
	}, nil
}

// GetDocument gets the document.
func (s *Server) GetDocument(
	ctx context.Context,
	req *api.GetDocumentRequest,
) (*api.GetDocumentResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.ProjectName)
	if err != nil {
		return nil, err
	}

	document, err := documents.GetDocumentSummary(
		ctx,
		s.backend,
		project,
		key.Key(req.DocumentKey),
	)
	if err != nil {
		return nil, err
	}

	pbDocument, err := converter.ToDocumentSummary(document)
	if err != nil {
		return nil, err
	}

	return &api.GetDocumentResponse{
		Document: pbDocument,
	}, nil
}

// GetSnapshotMeta gets the snapshot metadata that corresponds to the server sequence.
func (s *Server) GetSnapshotMeta(
	ctx context.Context,
	req *api.GetSnapshotMetaRequest,
) (*api.GetSnapshotMetaResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.ProjectName)
	if err != nil {
		return nil, err
	}

	doc, err := documents.GetDocumentByServerSeq(
		ctx,
		s.backend,
		project,
		key.Key(req.DocumentKey),
		req.ServerSeq,
	)
	if err != nil {
		return nil, err
	}

	snapshot, err := converter.ObjectToBytes(doc.RootObject())
	if err != nil {
		return nil, err
	}

	return &api.GetSnapshotMetaResponse{
		Lamport:  doc.Lamport(),
		Snapshot: snapshot,
	}, nil
}

// ListDocuments lists documents.
func (s *Server) ListDocuments(
	ctx context.Context,
	req *api.ListDocumentsRequest,
) (*api.ListDocumentsResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.ProjectName)
	if err != nil {
		return nil, err
	}

	docs, err := documents.ListDocumentSummaries(
		ctx,
		s.backend,
		project,
		types.Paging[types.ID]{
			Offset:    types.ID(req.PreviousId),
			PageSize:  int(req.PageSize),
			IsForward: req.IsForward,
		},
	)
	if err != nil {
		return nil, err
	}

	pbDocuments, err := converter.ToDocumentSummaries(docs)
	if err != nil {
		return nil, err
	}

	return &api.ListDocumentsResponse{
		Documents: pbDocuments,
	}, nil
}

// SearchDocuments searches documents for a specified string.
func (s *Server) SearchDocuments(
	ctx context.Context,
	req *api.SearchDocumentsRequest,
) (*api.SearchDocumentsResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.ProjectName)
	if err != nil {
		return nil, err
	}

	docs, err := documents.SearchDocumentSummaries(
		ctx,
		s.backend,
		project,
		req.Query,
		types.Paging[types.ID]{
			Offset:    types.ID(req.PreviousId),
			PageSize:  int(req.PageSize),
			IsForward: req.IsForward,
		},
	)
	if err != nil {
		return nil, err
	}

	pbDocuments, err := converter.ToDocumentSummaries(docs)
	if err != nil {
		return nil, err
	}

	return &api.SearchDocumentsResponse{
		Documents: pbDocuments,
	}, nil
}

// ListChanges lists of changes for the given document.
func (s *Server) ListChanges(
	ctx context.Context,
	req *api.ListChangesRequest,
) (*api.ListChangesResponse, error) {
	project, err := projects.GetProject(ctx, s.backend, req.ProjectName)
	if err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(
		ctx,
		s.backend,
		project,
		key.Key(req.DocumentKey),
	)
	if err != nil {
		return nil, err
	}
	lastSeq := docInfo.ServerSeq

	from, to := types.GetChangesRange(types.Paging[uint64]{
		Offset:    req.PreviousSeq,
		PageSize:  int(req.PageSize),
		IsForward: req.IsForward,
	}, lastSeq)

	changes, err := packs.FindChanges(
		ctx,
		s.backend,
		docInfo,
		from,
		to,
	)
	if err != nil {
		return nil, err
	}

	pbChanges, err := converter.ToChanges(changes)
	if err != nil {
		return nil, err
	}

	return &api.ListChangesResponse{
		Changes: pbChanges,
	}, nil
}
