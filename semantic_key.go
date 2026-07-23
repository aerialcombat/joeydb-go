package joeydb

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash"
)

// SemanticKeyDomain permanently identifies the semantic idempotency-key
// derivation defined by SemanticKey.
//
// It is deliberately independent of write.EncodingDomain: semantic operation
// identity and exact request-body identity are separate contracts.
const SemanticKeyDomain = "github.com/aerialcombat/joeydb-go/semantic-key/v1"

const maxSemanticKeyNamespaceBytes = 32

// SemanticKey derives a fixed-size logical WriteKey suffix for one
// application-defined business operation.
//
// Namespace must be a low-cardinality operation category containing 1-32
// lowercase ASCII letters, digits, '.', '_', or '-', starting with a letter.
// Parts are ordered and length-framed. Empty parts are valid and differ from
// missing parts. The caller owns each part's business meaning and must use the
// same namespace and parts when reconciling an uncertain operation.
//
// Session applies and validates the pinned JoeyDB epoch prefix. SemanticKey
// never hashes request bytes, applies a prefix, changes log identity, or alters
// retry policy.
func SemanticKey(namespace string, parts ...string) (WriteKey, error) {
	if !validSemanticKeyNamespace(namespace) {
		return WriteKey{}, &InvalidKeyError{
			Key: namespace,
			Reason: "semantic namespace must be 1-32 bytes, start with a lowercase letter, " +
				"and use only lowercase letters, digits, '.', '_', or '-'",
		}
	}

	digest := sha256.New()
	writeSemanticFrame(digest, []byte(SemanticKeyDomain))
	writeSemanticFrame(digest, []byte(namespace))
	writeSemanticUint64(digest, uint64(len(parts)))
	for _, part := range parts {
		writeSemanticFrame(digest, []byte(part))
	}

	suffix := namespace + ":" +
		base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
	return KeySuffix(suffix), nil
}

// Suffix returns the logical suffix and true only when key was constructed by
// KeySuffix or SemanticKey. It returns "", false for a FullKey or zero key.
//
// A SemanticKey suffix contains only its caller-chosen namespace and a one-way
// digest, so it is safe to log when the namespace is non-sensitive. This
// method does not make arbitrary KeySuffix values non-sensitive.
func (key WriteKey) Suffix() (string, bool) {
	if key.tag != writeKeySuffix {
		return "", false
	}
	return key.value, true
}

func validSemanticKeyNamespace(namespace string) bool {
	if len(namespace) == 0 || len(namespace) > maxSemanticKeyNamespaceBytes {
		return false
	}
	if namespace[0] < 'a' || namespace[0] > 'z' {
		return false
	}
	for i := 1; i < len(namespace); i++ {
		switch value := namespace[i]; {
		case value >= 'a' && value <= 'z':
		case value >= '0' && value <= '9':
		case value == '.', value == '_', value == '-':
		default:
			return false
		}
	}
	return true
}

func writeSemanticFrame(digest hash.Hash, value []byte) {
	writeSemanticUint64(digest, uint64(len(value)))
	_, _ = digest.Write(value)
}

func writeSemanticUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}
