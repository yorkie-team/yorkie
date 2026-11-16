/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package auth

import (
	"context"
	"errors"
	"net/http"
)

// errInvalidState is returned by a provider when consumeState refuses the
// state of a callback: it was not issued for this provider route, was already
// used, or expired.
var errInvalidState = errors.New("invalid state")

// errLoginRejected is wrapped by a provider error when the identity source
// sent back a login that does not verify.
var errLoginRejected = errors.New("login rejected")

// errInvalidUsername is wrapped by a provider error when the username of a
// new user is not a valid Yorkie username.
var errInvalidUsername = errors.New("invalid username")

// Identity is a user identity that a provider verified.
type Identity struct {
	// Subject is the ID of the user in the provider. It must not change for
	// the same person and must never be given to another person.
	Subject string

	// Username is the username of the user when the login creates the user.
	// A returning user keeps the username it was created with.
	Username string
}

// Provider signs users in to the dashboard through an identity source
// outside Yorkie. The AuthManager issues the login state, finds or creates
// the user, and sets the session cookie; a provider only talks to its
// identity source.
type Provider interface {
	// Name returns the auth provider recorded on the users it signs in.
	Name() string

	// LoginURL returns the URL of the identity source that starts a login
	// carrying the given state.
	LoginURL(state string) string

	// Callback verifies the login that the identity source sent back and
	// returns the identity of the user. It must verify the request, consume
	// the state carried in it with consumeState, and return errInvalidState
	// when consumeState reports false, all before returning an identity.
	// A provider whose state comes in a signed token verifies the signature
	// first, so an unsigned token cannot burn states. The manager refuses an
	// identity when no state was consumed, but it cannot tell whether the
	// consumed state is the one the request carried.
	Callback(ctx context.Context, r *http.Request, consumeState func(state string) bool) (*Identity, error)
}
