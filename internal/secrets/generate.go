// Package secrets generates secret values.
package secrets

import "crypto/rand"

// Generate returns a new random secret value with 130 bits of entropy. It uses only A–Z and
// 2–7 (base32), so it can be placed in connection URIs, shell commands and YAML without
// escaping.
func Generate() string {
	return rand.Text()
}
