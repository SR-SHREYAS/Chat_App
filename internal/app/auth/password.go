package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordHashVersion = "v1"
	passwordSaltSize    = 16
	passwordIterations  = 120000
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	digest := derivePasswordDigest(password, salt, passwordIterations)
	return fmt.Sprintf(
		"%s$%d$%s$%s",
		passwordHashVersion,
		passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return false, errors.New("invalid password hash format")
	}
	if parts[0] != passwordHashVersion {
		return false, errors.New("unsupported password hash version")
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false, errors.New("invalid password hash iterations")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errors.New("invalid password hash salt")
	}
	storedDigest, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, errors.New("invalid password hash digest")
	}

	computedDigest := derivePasswordDigest(password, salt, iterations)
	if len(computedDigest) != len(storedDigest) {
		return false, nil
	}

	return subtle.ConstantTimeCompare(computedDigest, storedDigest) == 1, nil
}

func derivePasswordDigest(password string, salt []byte, iterations int) []byte {
	input := make([]byte, 0, len(password)+len(salt)+32)
	input = append(input, []byte(password)...)
	input = append(input, salt...)
	sum := sha256.Sum256(input)
	digest := sum[:]

	for i := 1; i < iterations; i++ {
		input = input[:0]
		input = append(input, digest...)
		input = append(input, salt...)
		input = append(input, password...)
		sum = sha256.Sum256(input)
		digest = sum[:]
	}

	result := make([]byte, len(digest))
	copy(result, digest)
	return result
}
