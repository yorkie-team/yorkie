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

package database

import (
	"time"

	"github.com/lithammer/shortuuid/v4"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/units"
)

// ZeroID represents the minimum possible ID value, used as a starting point.
var ZeroID = types.ID("000000000000000000000000")

// DefaultProjectID is the default project ID.
var DefaultProjectID = ZeroID

// DefaultProjectName is the default project name.
var DefaultProjectName = "default"

const (
	DefaultAuthWebhookMaxRetries      uint64        = 10
	DefaultAuthWebhookMaxWaitInterval time.Duration = 3 * time.Second
	DefaultAuthWebhookMinWaitInterval time.Duration = 100 * time.Millisecond
	DefaultAuthWebhookRequestTimeout  time.Duration = 3 * time.Second

	DefaultEventWebhookMaxRetries      uint64        = 10
	DefaultEventWebhookMaxWaitInterval time.Duration = 3 * time.Second
	DefaultEventWebhookMinWaitInterval time.Duration = 100 * time.Millisecond
	DefaultEventWebhookRequestTimeout  time.Duration = 3 * time.Second

	DefaultClientDeactivateThreshold time.Duration = 24 * time.Hour
)

// ProjectInfo is a struct for project information.
type ProjectInfo struct {
	// ID is the unique ID of the project.
	ID types.ID `bson:"_id"`

	// Name is the name of this project.
	Name string `bson:"name"`

	// Owner is the owner of this project.
	Owner types.ID `bson:"owner"`

	// PublicKey is the API key of this project.
	PublicKey string `bson:"public_key"`

	// SecretKey is the secret key of this project.
	SecretKey string `bson:"secret_key"`

	// AuthWebhookURL is the url of the authorization webhook.
	AuthWebhookURL string `bson:"auth_webhook_url"`

	// AuthWebhookMethods is the methods that run the authorization webhook.
	AuthWebhookMethods []string `bson:"auth_webhook_methods"`

	// AuthWebhookMaxRetries is the max count that retries the authorization webhook.
	AuthWebhookMaxRetries uint64 `bson:"auth_webhook_max_retries"`

	// AuthWebhookMinWaitInterval is the min interval that waits before retrying the authorization webhook.
	AuthWebhookMinWaitInterval string `bson:"auth_webhook_min_wait_interval"`

	// AuthWebhookMaxWaitInterval is the max interval that waits before retrying the authorization webhook.
	AuthWebhookMaxWaitInterval string `bson:"auth_webhook_max_wait_interval"`

	// AuthWebhookRequestTimeout is the max waiting time per auth webhook request.
	AuthWebhookRequestTimeout string `bson:"auth_webhook_request_timeout"`

	// EventWebhookURL is the URL of the event webhook.
	EventWebhookURL string `bson:"event_webhook_url"`

	// EventWebhookEvents is the events that the event webhook listens to.
	EventWebhookEvents []string `bson:"event_webhook_events"`

	// EventWebhookMaxRetries is the max count that retries the event webhook.
	EventWebhookMaxRetries uint64 `bson:"event_webhook_max_retries"`

	// EventWebhookMinWaitInterval is the min interval that waits before retrying the event webhook.
	EventWebhookMinWaitInterval string `bson:"event_webhook_min_wait_interval"`

	// EventWebhookMaxWaitInterval is the max interval that waits before retrying the event webhook.
	EventWebhookMaxWaitInterval string `bson:"event_webhook_max_wait_interval"`

	// EventWebhookRequestTimeout is the max waiting time per event webhook request.
	EventWebhookRequestTimeout string `bson:"event_webhook_request_timeout"`

	// ClientDeactivateThreshold is the time after which clients in
	// specific project are considered deactivate for housekeeping.
	ClientDeactivateThreshold string `bson:"client_deactivate_threshold"`

	// MaxSubscribersPerDocument is the maximum number of subscribers per document.
	// If it is 0, there is no limit.
	MaxSubscribersPerDocument int `bson:"max_subscribers_per_document"`

	// MaxAttachmentsPerDocument is the maximum number of attachments per document.
	// If it is 0, there is no limit.
	MaxAttachmentsPerDocument int `bson:"max_attachments_per_document"`

	// MaxSizePerDocument is the maximum size of a document in bytes.
	MaxSizePerDocument int `bson:"max_size_per_document"`

	// RemoveOnDetach is the flag to remove the document on detach.
	RemoveOnDetach bool `bson:"remove_on_detach"`

	// AllowedOrigins is the list of allowed origins.
	AllowedOrigins []string `bson:"allowed_origins"`

	// CreatedAt is the time when the project was created.
	CreatedAt time.Time `bson:"created_at"`

	// UpdatedAt is the time when the project was updated.
	UpdatedAt time.Time `bson:"updated_at"`
}

// NewProjectInfo creates a new ProjectInfo with the given name and owner.
// 
// The returned ProjectInfo is initialized with sensible defaults for webhook
// settings (authorization and event webhooks), client inactivity threshold,
// size/attachment/subscriber limits, and generated API keys. CreatedAt is set
// to the current time.
func NewProjectInfo(name string, owner types.ID) *ProjectInfo {
	return &ProjectInfo{
		Name:                        name,
		Owner:                       owner,
		AuthWebhookMaxRetries:       DefaultAuthWebhookMaxRetries,
		AuthWebhookMinWaitInterval:  DefaultAuthWebhookMinWaitInterval.String(),
		AuthWebhookMaxWaitInterval:  DefaultAuthWebhookMaxWaitInterval.String(),
		AuthWebhookRequestTimeout:   DefaultAuthWebhookRequestTimeout.String(),
		EventWebhookMaxRetries:      DefaultEventWebhookMaxRetries,
		EventWebhookMinWaitInterval: DefaultEventWebhookMinWaitInterval.String(),
		EventWebhookMaxWaitInterval: DefaultEventWebhookMaxWaitInterval.String(),
		EventWebhookRequestTimeout:  DefaultEventWebhookRequestTimeout.String(),
		ClientDeactivateThreshold:   DefaultClientDeactivateThreshold.String(),
		MaxSubscribersPerDocument:   0,
		MaxAttachmentsPerDocument:   0,
		MaxSizePerDocument:          10 * units.MiB,
		RemoveOnDetach:              false,
		PublicKey:                   shortuuid.New(),
		SecretKey:                   shortuuid.New(),
		CreatedAt:                   time.Now(),
	}
}

// DeepCopy returns a deep copy of the ProjectInfo.
func (i *ProjectInfo) DeepCopy() *ProjectInfo {
	if i == nil {
		return nil
	}

	return &ProjectInfo{
		ID:                          i.ID,
		Name:                        i.Name,
		Owner:                       i.Owner,
		PublicKey:                   i.PublicKey,
		SecretKey:                   i.SecretKey,
		AuthWebhookURL:              i.AuthWebhookURL,
		AuthWebhookMethods:          i.AuthWebhookMethods,
		AuthWebhookMaxRetries:       i.AuthWebhookMaxRetries,
		AuthWebhookMinWaitInterval:  i.AuthWebhookMinWaitInterval,
		AuthWebhookMaxWaitInterval:  i.AuthWebhookMaxWaitInterval,
		AuthWebhookRequestTimeout:   i.AuthWebhookRequestTimeout,
		EventWebhookURL:             i.EventWebhookURL,
		EventWebhookEvents:          i.EventWebhookEvents,
		EventWebhookMaxRetries:      i.EventWebhookMaxRetries,
		EventWebhookMinWaitInterval: i.EventWebhookMinWaitInterval,
		EventWebhookMaxWaitInterval: i.EventWebhookMaxWaitInterval,
		EventWebhookRequestTimeout:  i.EventWebhookRequestTimeout,
		ClientDeactivateThreshold:   i.ClientDeactivateThreshold,
		MaxSubscribersPerDocument:   i.MaxSubscribersPerDocument,
		MaxAttachmentsPerDocument:   i.MaxAttachmentsPerDocument,
		MaxSizePerDocument:          i.MaxSizePerDocument,
		RemoveOnDetach:              i.RemoveOnDetach,
		AllowedOrigins:              i.AllowedOrigins,
		CreatedAt:                   i.CreatedAt,
		UpdatedAt:                   i.UpdatedAt,
	}
}

// UpdateFields updates the fields.
func (i *ProjectInfo) UpdateFields(fields *types.UpdatableProjectFields) {
	if fields.Name != nil {
		i.Name = *fields.Name
	}
	if fields.AuthWebhookURL != nil {
		i.AuthWebhookURL = *fields.AuthWebhookURL
	}
	if fields.AuthWebhookMethods != nil {
		i.AuthWebhookMethods = *fields.AuthWebhookMethods
	}
	if fields.AuthWebhookMaxRetries != nil {
		i.AuthWebhookMaxRetries = *fields.AuthWebhookMaxRetries
	}
	if fields.AuthWebhookMinWaitInterval != nil {
		i.AuthWebhookMinWaitInterval = *fields.AuthWebhookMinWaitInterval
	}
	if fields.AuthWebhookMaxWaitInterval != nil {
		i.AuthWebhookMaxWaitInterval = *fields.AuthWebhookMaxWaitInterval
	}
	if fields.AuthWebhookRequestTimeout != nil {
		i.AuthWebhookRequestTimeout = *fields.AuthWebhookRequestTimeout
	}
	if fields.EventWebhookURL != nil {
		i.EventWebhookURL = *fields.EventWebhookURL
	}
	if fields.EventWebhookEvents != nil {
		i.EventWebhookEvents = *fields.EventWebhookEvents
	}
	if fields.EventWebhookMaxRetries != nil {
		i.EventWebhookMaxRetries = *fields.EventWebhookMaxRetries
	}
	if fields.EventWebhookMinWaitInterval != nil {
		i.EventWebhookMinWaitInterval = *fields.EventWebhookMinWaitInterval
	}
	if fields.EventWebhookMaxWaitInterval != nil {
		i.EventWebhookMaxWaitInterval = *fields.EventWebhookMaxWaitInterval
	}
	if fields.EventWebhookRequestTimeout != nil {
		i.EventWebhookRequestTimeout = *fields.EventWebhookRequestTimeout
	}
	if fields.ClientDeactivateThreshold != nil {
		i.ClientDeactivateThreshold = *fields.ClientDeactivateThreshold
	}
	if fields.MaxSubscribersPerDocument != nil {
		i.MaxSubscribersPerDocument = *fields.MaxSubscribersPerDocument
	}
	if fields.MaxAttachmentsPerDocument != nil {
		i.MaxAttachmentsPerDocument = *fields.MaxAttachmentsPerDocument
	}
	if fields.MaxSizePerDocument != nil {
		i.MaxSizePerDocument = *fields.MaxSizePerDocument
	}
	if fields.RemoveOnDetach != nil {
		i.RemoveOnDetach = *fields.RemoveOnDetach
	}
	if fields.AllowedOrigins != nil {
		i.AllowedOrigins = *fields.AllowedOrigins
	}
}

// ToProject converts the ProjectInfo to the Project.
func (i *ProjectInfo) ToProject() *types.Project {
	return &types.Project{
		ID:                          i.ID,
		Name:                        i.Name,
		Owner:                       i.Owner,
		AuthWebhookURL:              i.AuthWebhookURL,
		AuthWebhookMethods:          i.AuthWebhookMethods,
		AuthWebhookMaxRetries:       i.AuthWebhookMaxRetries,
		AuthWebhookMinWaitInterval:  i.AuthWebhookMinWaitInterval,
		AuthWebhookMaxWaitInterval:  i.AuthWebhookMaxWaitInterval,
		AuthWebhookRequestTimeout:   i.AuthWebhookRequestTimeout,
		EventWebhookURL:             i.EventWebhookURL,
		EventWebhookEvents:          i.EventWebhookEvents,
		EventWebhookMaxRetries:      i.EventWebhookMaxRetries,
		EventWebhookMinWaitInterval: i.EventWebhookMinWaitInterval,
		EventWebhookMaxWaitInterval: i.EventWebhookMaxWaitInterval,
		EventWebhookRequestTimeout:  i.EventWebhookRequestTimeout,
		ClientDeactivateThreshold:   i.ClientDeactivateThreshold,
		MaxSubscribersPerDocument:   i.MaxSubscribersPerDocument,
		MaxAttachmentsPerDocument:   i.MaxAttachmentsPerDocument,
		MaxSizePerDocument:          i.MaxSizePerDocument,
		RemoveOnDetach:              i.RemoveOnDetach,
		AllowedOrigins:              i.AllowedOrigins,
		PublicKey:                   i.PublicKey,
		SecretKey:                   i.SecretKey,
		CreatedAt:                   i.CreatedAt,
		UpdatedAt:                   i.UpdatedAt,
	}
}
