package cluster

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The identity a keyless signature has when shelf's own workflows made it. With a reusable
// workflow, the certificate's subject is the reusable workflow (build.yml of tweinmann/shelf at
// the ref the tenant uses), not the tenant's repository; which tenant it belongs to follows from
// who may write to the registry path.
const (
	DefaultSignerIssuer  = "https://token.actions.githubusercontent.com"
	DefaultSignerSubject = `^https://github\.com/tweinmann/shelf/\.github/workflows/(build|release)\.yml@refs/(tags|heads)/.+$`
)

// The public instance of Sigstore that keyless signatures are checked against.
const (
	fulcioURL = "https://fulcio.sigstore.dev"
	rekorURL  = "https://rekor.sigstore.dev"
)

// Where a signer key lives: shelf-system/signer-key, copied by the platform into every app and
// preview namespace as SignerSecretName, and written into flux-system for the platform itself.
// In keyless mode the Secret exists too, without a key, so the copy always has a source.
const (
	SignerSecretName = "shelf-signer"
	signerKeySecret  = "signer-key"
	signerKeyFile    = "cosign.pub"
)

// imagePolicyGVK is the Sigstore policy-controller's ClusterImagePolicy. The platform gives every
// app one, ImagePolicyName, for the images built from its package.
var imagePolicyGVK = schema.GroupVersionKind{Group: "policy.sigstore.dev", Version: "v1beta1", Kind: "ClusterImagePolicy"}

// ImagePolicyName is the ClusterImagePolicy of an app.
func ImagePolicyName(app string) string { return "shelf-" + app }

// Signer is who must have signed every artifact and built image the cluster runs: an identity
// for keyless signatures, or a cosign public key. There is no signer that accepts everything.
type Signer struct {
	// Issuer and Subject are the OIDC issuer and a regular expression for the certificate's
	// subject of a keyless signature. They are ignored when Key is set.
	Issuer  string
	Subject string
	// Key is a PEM-encoded public key. A cluster with a key accepts only signatures made with
	// it, without a transparency log; it is meant for clusters that test shelf.
	Key string
}

// DefaultSigner accepts keyless signatures made by shelf's build and release workflows.
func DefaultSigner() Signer {
	return Signer{Issuer: DefaultSignerIssuer, Subject: DefaultSignerSubject}
}

// Mode is "key" or "keyless".
func (s Signer) Mode() string {
	if s.Key != "" {
		return "key"
	}
	return "keyless"
}

// IsZero reports whether no signer is set, as in a cluster installed before Phase 11.
func (s Signer) IsZero() bool { return s.Key == "" && s.Issuer == "" && s.Subject == "" }

func (s Signer) String() string {
	if s.Key != "" {
		return "cosign key " + keyFingerprint(s.Key)
	}
	return fmt.Sprintf("keyless, issuer %s, subject %s", s.Issuer, s.Subject)
}

// Check reports a signer that cannot verify anything.
func (s Signer) Check() error {
	if s.Key != "" {
		_, err := ParseSignerKey([]byte(s.Key))
		return err
	}
	if s.Issuer == "" || s.Subject == "" {
		return errors.New("a keyless signer needs an issuer and a subject")
	}
	if _, err := regexp.Compile(s.Subject); err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	return nil
}

// ParseSignerKey checks that data is one PEM-encoded public key and returns it normalised.
func ParseSignerKey(data []byte) (string, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return "", errors.New("not a PEM public key (-----BEGIN PUBLIC KEY-----)")
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return "", errors.New("more than one PEM block")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return "", fmt.Errorf("not a public key: %w", err)
	}
	return string(pem.EncodeToMemory(block)), nil
}

// keyFingerprint names a key in output: the first bytes of the SHA-256 of its DER encoding.
func keyFingerprint(key string) string {
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return "(unreadable)"
	}
	sum := sha256.Sum256(block.Bytes)
	return "sha256:" + hex.EncodeToString(sum[:8])
}

// verify is the spec.verify of a Flux OCIRepository that accepts what this signer signed.
// Flux matches issuer and subject as regular expressions.
func (s Signer) verify(secretName string) map[string]any {
	if s.Key != "" {
		return map[string]any{
			"provider":  "cosign",
			"secretRef": map[string]any{"name": secretName},
		}
	}
	return map[string]any{
		"provider": "cosign",
		"matchOIDCIdentity": []any{map[string]any{
			"issuer":  "^" + regexp.QuoteMeta(s.Issuer) + "$",
			"subject": s.Subject,
		}},
	}
}

// authority is the authority of a Sigstore ClusterImagePolicy that admits what this signer
// signed. A key authority without a ctlog looks up no transparency log, as dev signatures have
// no entry there.
func (s Signer) authority() map[string]any {
	if s.Key != "" {
		return map[string]any{"key": map[string]any{"data": s.Key}}
	}
	return map[string]any{
		"keyless": map[string]any{
			"url": fulcioURL,
			"identities": []any{map[string]any{
				"issuer":        s.Issuer,
				"subjectRegExp": s.Subject,
			}},
		},
		"ctlog": map[string]any{"url": rekorURL},
	}
}

// signerSecret holds the signer's key, empty for a keyless signer. It is written as data, not
// stringData, which the API server never returns, so that applying it again changes nothing.
func signerSecret(namespace, name string, s Signer) *unstructured.Unstructured {
	data := map[string]any{}
	if s.Key != "" {
		data[signerKeyFile] = base64.StdEncoding.EncodeToString([]byte(s.Key))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{WatchLabel: "Enabled"},
		},
		"type": "Opaque",
		"data": data,
	}}
}

// compactJSON is what the cluster settings carry for the platform: Flux substitutes the value
// into the ResourceSets, where a JSON object is read as the YAML mapping it is.
func compactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}
