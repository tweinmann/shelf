package hostcfg

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The password of the admin UI is hashed with PBKDF2-SHA256. argon2id would resist a stolen
// hash better, but it is not in the standard library, and one password on a machine in a home
// network does not justify a dependency. The algorithm is part of the stored string, so a later
// change can recognise old hashes and upgrade them on the next login.
const (
	hashAlgorithm = "pbkdf2-sha256"
	// hashIterations follows the OWASP recommendation for PBKDF2-SHA256.
	hashIterations = 600_000
	hashLength     = 32
	saltLength     = 16
	// MinPasswordLength is the shortest password the admin UI accepts.
	MinPasswordLength = 8
)

// ErrPasswordTooShort is returned for a password below MinPasswordLength.
var ErrPasswordTooShort = fmt.Errorf("the password needs at least %d characters", MinPasswordLength)

// HashPassword returns the string to store for a password.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, hashLength)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		hashAlgorithm,
		strconv.Itoa(hashIterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	}, "$"), nil
}

// CheckPassword reports whether password belongs to the stored hash. It takes the same time
// whether the password is wrong or right.
func CheckPassword(stored, password string) bool {
	algorithm, iterations, salt, want, err := parseHash(stored)
	if err != nil {
		return false
	}
	if algorithm != hashAlgorithm {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parseHash(stored string) (algorithm string, iterations int, salt, sum []byte, err error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 {
		return "", 0, nil, nil, errors.New("not a password hash")
	}
	iterations, err = strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return "", 0, nil, nil, errors.New("bad iteration count")
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[2]); err != nil {
		return "", 0, nil, nil, err
	}
	if sum, err = base64.RawStdEncoding.DecodeString(parts[3]); err != nil {
		return "", 0, nil, nil, err
	}
	if len(sum) == 0 {
		return "", 0, nil, nil, errors.New("empty hash")
	}
	return parts[0], iterations, salt, sum, nil
}

// NewToken returns a random secret for a setup token or a session id.
func NewToken() string { return rand.Text() }
