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
 *
 */

package types

// AuthorizationKey is the key of the authorization header.
const AuthorizationKey = "authorization"

// UserAgentKey is the key of the user agent header.
const UserAgentKey = "x-yorkie-user-agent"

// APIKeyKey is the key of the api key header.
const APIKeyKey = "x-api-key"

// ShardKey is the key of the shard header.
const ShardKey = "x-shard-key"

// GoSDKType is the type part of Go SDK in value of UserAgent.
const GoSDKType = "yorkie-go-sdk"
