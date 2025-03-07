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
	"errors"
	"time"

	"github.com/lithammer/shortuuid/v4"

	"github.com/yorkie-team/yorkie/api/types"
)

// ErrInvalidTimeDurationString is returned when the given time duration string is not in valid format.
var ErrInvalidTimeDurationString = errors.New("invalid time duration string format")

// DefaultProjectID is the default project ID.
var DefaultProjectID = types.ID("000000000000000000000000")

// DefaultProjectName is the default project name.
var DefaultProjectName = "default"

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

	// EventWebhookURL is the URL of the event webhook.
	EventWebhookURL string `bson:"event_webhook_url"`

	// EventWebhookEvents is the events that the event webhook listens to.
	EventWebhookEvents []string `bson:"event_webhook_events"`

	// ClientDeactivateThreshold is the time after which clients in
	// specific project are considered deactivate for housekeeping.
	ClientDeactivateThreshold string `bson:"client_deactivate_threshold"`

	// SubscriptionCountLimitPerDocument is limit of subscription count for each document. 0 means no limit.
	SubscriptionCountLimitPerDocument int `bson:"subscription_count_limit_per_document"`

	// CreatedAt is the time when the project was created.
	CreatedAt time.Time `bson:"created_at"`

	// UpdatedAt is the time when the project was updated.
	UpdatedAt time.Time `bson:"updated_at"`
}

// NewProjectInfo creates a new ProjectInfo of the given name.
func NewProjectInfo(name string, owner types.ID, clientDeactivateThreshold string) *ProjectInfo {
	return &ProjectInfo{
		Name:                              name,
		Owner:                             owner,
		ClientDeactivateThreshold:         clientDeactivateThreshold,
		SubscriptionCountLimitPerDocument: 0,
		PublicKey:                         shortuuid.New(),
		SecretKey:                         shortuuid.New(),
		CreatedAt:                         time.Now(),
	}
}

// DeepCopy returns a deep copy of the ProjectInfo.
func (i *ProjectInfo) DeepCopy() *ProjectInfo {
	if i == nil {
		return nil
	}

	return &ProjectInfo{
		ID:                                i.ID,
		Name:                              i.Name,
		Owner:                             i.Owner,
		PublicKey:                         i.PublicKey,
		SecretKey:                         i.SecretKey,
		AuthWebhookURL:                    i.AuthWebhookURL,
		AuthWebhookMethods:                i.AuthWebhookMethods,
		EventWebhookURL:                   i.EventWebhookURL,
		EventWebhookEvents:                i.EventWebhookEvents,
		ClientDeactivateThreshold:         i.ClientDeactivateThreshold,
		SubscriptionCountLimitPerDocument: i.SubscriptionCountLimitPerDocument,
		CreatedAt:                         i.CreatedAt,
		UpdatedAt:                         i.UpdatedAt,
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
	if fields.EventWebhookURL != nil {
		i.EventWebhookURL = *fields.EventWebhookURL
	}
	if fields.EventWebhookEvents != nil {
		i.EventWebhookEvents = *fields.EventWebhookEvents
	}
	if fields.ClientDeactivateThreshold != nil {
		i.ClientDeactivateThreshold = *fields.ClientDeactivateThreshold
	}
	if fields.SubscriptionCountLimitPerDocument != nil {
		i.SubscriptionCountLimitPerDocument = *fields.SubscriptionCountLimitPerDocument
	}
}

// ToProject converts the ProjectInfo to the Project.
func (i *ProjectInfo) ToProject() *types.Project {
	return &types.Project{
		ID:                                i.ID,
		Name:                              i.Name,
		Owner:                             i.Owner,
		AuthWebhookURL:                    i.AuthWebhookURL,
		AuthWebhookMethods:                i.AuthWebhookMethods,
		EventWebhookURL:                   i.EventWebhookURL,
		EventWebhookEvents:                i.EventWebhookEvents,
		ClientDeactivateThreshold:         i.ClientDeactivateThreshold,
		SubscriptionCountLimitPerDocument: i.SubscriptionCountLimitPerDocument,
		PublicKey:                         i.PublicKey,
		SecretKey:                         i.SecretKey,
		CreatedAt:                         i.CreatedAt,
		UpdatedAt:                         i.UpdatedAt,
	}
}

// ClientDeactivateThresholdAsTimeDuration converts ClientDeactivateThreshold string to time.Duration.
func (i *ProjectInfo) ClientDeactivateThresholdAsTimeDuration() (time.Duration, error) {
	clientDeactivateThreshold, err := time.ParseDuration(i.ClientDeactivateThreshold)
	if err != nil {
		return 0, ErrInvalidTimeDurationString
	}

	return clientDeactivateThreshold, nil
}
