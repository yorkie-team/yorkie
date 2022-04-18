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

package db

import "time"

// ProjectInfo is a struct for project information.
type ProjectInfo struct {
	// ID is the unique ID of the project.
	ID ID `bson:"_id"`

	// Name is the name of this project.
	Name string `bson:"name"`

	// ClientAPIKey is the client API key of this project.
	ClientAPIKey string `bson:"client_api_key"`

	// CreatedAt is the time when the project was created.
	CreatedAt time.Time `bson:"created_at"`
}

// DeepCopy returns a deep copy of the ProjectInfo.
func (i *ProjectInfo) DeepCopy() *ProjectInfo {
	if i == nil {
		return nil
	}

	return &ProjectInfo{
		ID:           i.ID,
		Name:         i.Name,
		ClientAPIKey: i.ClientAPIKey,
		CreatedAt:    i.CreatedAt,
	}
}
