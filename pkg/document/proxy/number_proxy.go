/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package proxy

import (
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
)

type NumberProxy struct {
	*json.Primitive
	context *change.Context
}

func NewNumberProxy(ctx *change.Context, primitive *json.Primitive) *NumberProxy {
	valueType := primitive.ValueType()
	if valueType != json.Integer && valueType != json.Long {
		panic("unsupported type")
	}
	return &NumberProxy{
		Primitive: primitive,
		context:   ctx,
	}
}

func (p *NumberProxy) Increase(v int) *NumberProxy {
	var primitive *json.Primitive
	tickect := p.context.IssueTimeTicket()
	if p.ValueType() == json.Long {
		primitive = json.NewPrimitive(int64(v), tickect)
	} else {
		primitive = json.NewPrimitive(v, tickect)
	}

	p.context.Push(operation.NewIncrease(
		p.CreatedAt(),
		primitive,
		tickect,
	))

	return p
}
