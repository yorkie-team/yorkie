/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package housekeeping is the package for housekeeping service. It cleans up
// the resources and data that is no longer needed.
package housekeeping

import (
	"fmt"
	"time"
)

// Config is the configuration for the housekeeping service.
type Config struct {
	// DeactivateCandidatesInterval is the time between housekeeping runs for deactivate candidates.
	DeactivateCandidatesInterval string `yaml:"DeactivateCandidatesInterval"`

	// DeleteDocumentsInterval is the time between housekeeping runs for document deletion.
	DeleteDocumentsInterval string `yaml:"DeleteDocumentsInterval"`

	// DocumentHardDeletionGracefulPeriod finds documents whose removed_at time is older than that time.
	DocumentHardDeletionGracefulPeriod time.Duration `yaml:"HousekeepingDocumentHardDeletionGracefulPeriod"`

	// ClientDeactivationCandidateLimitPerProject is the maximum number of candidates to be returned per project.
	ClientDeactivationCandidateLimitPerProject int `yaml:"ClientDeactivationCandidateLimitPerProject"`

	// DocumentHardDeletionCandidateLimitPerProject is the maximum number of candidates to be returned per project.
	DocumentHardDeletionCandidateLimitPerProject int `yaml:"DocumentHardDeletionCandidateLimitPerProject"`

	// ProjectFetchSize is the maximum number of projects to be returned to deactivate candidates.
	ProjectFetchSize int `yaml:"HousekeepingProjectFetchSize"`
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.DeactivateCandidatesInterval); err != nil {
		return fmt.Errorf(
			`invalid argument %s for "--housekeeping-interval-deactivate-candidates" flag: %w`,
			c.DeactivateCandidatesInterval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.DeleteDocumentsInterval); err != nil {
		return fmt.Errorf(
			`invalid argument %s for "--housekeeping-interval-delete-documents" flag: %w`,
			c.DeleteDocumentsInterval,
			err,
		)
	}

	if c.DocumentHardDeletionGracefulPeriod <= 0 {
		return fmt.Errorf(
			`invalid argument %v for "--housekeeping-project-delete-graceful-period"`,
			c.DocumentHardDeletionGracefulPeriod,
		)
	}

	if c.ClientDeactivationCandidateLimitPerProject <= 0 {
		return fmt.Errorf(
			`invalid argument %d for "--housekeeping-client-deactivateion-candidate-limit-per-project"`,
			c.ClientDeactivationCandidateLimitPerProject,
		)
	}

	if c.DocumentHardDeletionCandidateLimitPerProject <= 0 {
		return fmt.Errorf(
			`invalid argument %d for "--housekeeping-document-hard-deletion-limit-per-project"`,
			c.DocumentHardDeletionCandidateLimitPerProject,
		)
	}

	if c.ProjectFetchSize <= 0 {
		return fmt.Errorf(
			`invalid argument %d for "--housekeeping-project-fetc-size" flag`,
			c.ProjectFetchSize,
		)
	}

	return nil
}

// ParseInterval parses the interval.
func (c *Config) ParseInterval(
	interval string,
) (time.Duration, error) {
	parseInterval, err := time.ParseDuration(interval)
	if err != nil {
		return 0, fmt.Errorf("parse interval %s: %w", interval, err)
	}

	return parseInterval, nil
}
