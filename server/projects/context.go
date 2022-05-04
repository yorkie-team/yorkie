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

package projects

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
)

// projectKey is the key for the context.Context.
type projectKey struct{}

// From returns the project from the context.
func From(ctx context.Context) *types.Project {
	return ctx.Value(projectKey{}).(*types.Project)
}

// With creates a new context with the given Project.
func With(ctx context.Context, project *types.Project) context.Context {
	return context.WithValue(ctx, projectKey{}, project)
}
