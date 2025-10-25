package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// loadPrivateKey reads and parses a PEM-encoded RSA private key
func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read private key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block containing private key")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		pk, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("failed to parse private key (tried PKCS1 and PKCS8): %v / %v", err, err2)
		}

		var ok bool
		key, ok = pk.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not a valid RSA private key")
		}
	}
	return key, nil
}

func main() {
	// --- Configuration ---
	privateKeyPath := "private.pem"
	userID := "user-123-abc"
	expiresIn := time.Hour * 24

	log.Println("Loading private key...")
	privKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}

	claims := jwt.MapClaims{
		"sub":  userID,
		"name": "Test User",
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(expiresIn).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	tokenString, err := token.SignedString(privKey)
	if err != nil {
		log.Fatalf("Failed to sign token: %v", err)
	}

	log.Println("Token generated successfully!")
	fmt.Println(tokenString)
}
