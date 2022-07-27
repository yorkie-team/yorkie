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

package admin

import (
	"fmt"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var (
	// ErrUnexpectedSigningMethod is returned when the signing method is unexpected.
	ErrUnexpectedSigningMethod = fmt.Errorf("unexpected signing method")
)

// UserClaims is a JWT claims struct for a user.
type UserClaims struct {
	jwt.StandardClaims

	Email string `json:"email"`
}

// JWTManager manages JWT tokens.
type JWTManager struct {
	secretKey     string
	tokenDuration time.Duration
}

// NewJWTManager creates a new JWTManager.
func NewJWTManager(secretKey string, tokenDuration time.Duration) *JWTManager {
	return &JWTManager{
		secretKey:     secretKey,
		tokenDuration: tokenDuration,
	}
}

// GenerateToken generates a new JWT token for the user.
func (m *JWTManager) GenerateToken(email string) (string, error) {
	claims := UserClaims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(m.tokenDuration).Unix(),
		},
		Email: email,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.secretKey))
}

// VerifyToken verifies the JWT token.
func (m *JWTManager) VerifyToken(token string) (*UserClaims, error) {
	claims := &UserClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
		_, ok := token.Method.(*jwt.SigningMethodHMAC)
		if !ok {
			return nil, fmt.Errorf("%s: %w", token.Method.Alg(), ErrUnexpectedSigningMethod)
		}
		return []byte(m.secretKey), nil
	})
	if err != nil {
		return nil, err
	}

	return claims, nil
}
