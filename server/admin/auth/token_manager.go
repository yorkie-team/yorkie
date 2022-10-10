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

// Package auth provides the authentication and authorization of the admin server.
package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt"
)

var (
	// ErrUnexpectedSigningMethod is returned when the signing method is unexpected.
	ErrUnexpectedSigningMethod = fmt.Errorf("unexpected signing method")
)

// UserClaims is a JWT claims struct for a user.
type UserClaims struct {
	jwt.StandardClaims

	Username string `json:"username"`
}

// TokenManager manages JWT tokens.
type TokenManager struct {
	secretKey     string
	tokenDuration time.Duration
}

// NewTokenManager creates a new TokenManager.
func NewTokenManager(secretKey string, tokenDuration time.Duration) *TokenManager {
	return &TokenManager{
		secretKey:     secretKey,
		tokenDuration: tokenDuration,
	}
}

// Generate generates a new token for the user.
func (m *TokenManager) Generate(username string) (string, error) {
	claims := UserClaims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(m.tokenDuration).Unix(),
		},
		Username: username,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(m.secretKey))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return signedToken, nil
}

// Verify verifies the given token.
func (m *TokenManager) Verify(token string) (*UserClaims, error) {
	claims := &UserClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
		_, ok := token.Method.(*jwt.SigningMethodHMAC)
		if !ok {
			return nil, fmt.Errorf("%s: %w", token.Method.Alg(), ErrUnexpectedSigningMethod)
		}
		return []byte(m.secretKey), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	return claims, nil
}
