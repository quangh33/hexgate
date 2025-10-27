package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"log"
	"net/http"
	"os"
	"strings"
)

type contextKey string

const userIDKey contextKey = "userID"

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read public key file: %w", err)
	}

	// decode the public key
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block containing public key")
	}
	// parse from raw bytes (X.509 PKIX format) to a generic Go public key
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	key, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not a valid RSA public key")
	}
	return key, nil
}

// jwtAuthMiddleware validates an RS256 JWT
func jwtAuthMiddleware(next http.Handler, key *rsa.PublicKey) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "401 Unauthorized: Missing Authorization header", http.StatusUnauthorized)
			return
		}

		tokenString, found := strings.CutPrefix(authHeader, "Bearer ")
		if !found {
			http.Error(w, "401 Unauthorized: Invalid Authorization header format", http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return key, nil
		})

		if err != nil {
			log.Printf("Token validation error: %v", err)
			http.Error(w, "401 Unauthorized: Invalid token", http.StatusUnauthorized)
			return
		}

		if !token.Valid {
			http.Error(w, "401 Unauthorized: Invalid token", http.StatusUnauthorized)
			return
		}

		log.Printf("Claims: %v", token.Claims)
		userID, err := token.Claims.GetSubject()
		if err != nil {
			log.Printf("Token missing 'sub' claim: %v", err)
			http.Error(w, "401 Unauthorized: Invalid token claims", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		log.Println("JWT authenticated successfully")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
