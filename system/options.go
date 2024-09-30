package system

import (
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Option configures Options.
type Option func(*Options)

// Options configures how we set up the client.
type Options struct {
	// Key is the key of the client. It is used to identify the client.
	Key string

	// APIKey is the API key of the client.
	APIKey string

	// ActorID is the actorID of the client.
	ActorID *time.ActorID

	// Token is the token of the client. Each request will be authenticated with this token.
	Token string

	// CertFile is the path to the certificate file.
	CertFile string

	// ServerNameOverride is the server name override.
	ServerNameOverride string

	// Logger is the Logger of the client.
	Logger *zap.Logger

	// MaxCallRecvMsgSize is the maximum message size in bytes the client can receive.
	MaxCallRecvMsgSize int
}

// WithKey configures the key of the client.
func WithKey(key string) Option {
	return func(o *Options) { o.Key = key }
}

// WithAPIKey configures the API key of the client.
func WithAPIKey(apiKey string) Option {
	return func(o *Options) { o.APIKey = apiKey }
}

// WithToken configures the token of the client.
func WithToken(token string) Option {
	return func(o *Options) { o.Token = token }
}

// WithActorID configures the ActorID of the client.
func WithActorID(actorID *time.ActorID) Option {
	return func(o *Options) { o.ActorID = actorID }
}
