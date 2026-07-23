package joeydb

import (
	"context"
	"fmt"
	"strings"

	querypkg "github.com/aerialcombat/joeydb-go/query"
	writepkg "github.com/aerialcombat/joeydb-go/write"
)

type writeKeyTag uint8

const (
	writeKeyUnset writeKeyTag = iota
	writeKeySuffix
	writeKeyFull
)

// WriteKey distinguishes a logical key suffix from an already-complete wire
// key. Construct one with KeySuffix or FullKey.
type WriteKey struct {
	tag   writeKeyTag
	value string
}

// KeySuffix constructs the normal application-facing idempotency key form.
// Session.WriteRequest applies the pinned required prefix exactly once.
func KeySuffix(value string) WriteKey {
	return WriteKey{tag: writeKeySuffix, value: value}
}

// FullKey constructs the advanced form for a caller that already owns the
// complete wire key, including any advertised required prefix.
func FullKey(value string) WriteKey {
	return WriteKey{tag: writeKeyFull, value: value}
}

// QueryRequest validates and encodes a typed query before any network I/O.
func (c *Client) QueryRequest(
	ctx context.Context,
	request querypkg.Request,
	out any,
	options ...RequestOption,
) (*Response, error) {
	body, err := request.Encode()
	if err != nil {
		return nil, err
	}
	return c.Query(ctx, body, out, options...)
}

// WriteRequest validates and encodes a typed write once, checks its required
// advertised features, resolves the key, and delegates to the existing
// identity-pinned exact-body retry path.
func (s *Session) WriteRequest(
	ctx context.Context,
	key WriteKey,
	request writepkg.Request,
	out any,
	options ...RequestOption,
) (*Response, error) {
	body, err := request.Encode()
	if err != nil {
		return nil, err
	}
	if !s.requirements.Writable && !s.requirements.Ingestion {
		return nil, &CapabilityError{Reason: "session was not preflighted for writes"}
	}
	if err := s.validateWriteFeatures(request.Features()); err != nil {
		return nil, err
	}
	resolved, err := s.resolveWriteKey(key)
	if err != nil {
		return nil, err
	}
	return s.writeExact(ctx, body, resolved, out, false, options...)
}

func (s *Session) resolveWriteKey(key WriteKey) (string, error) {
	idempotency := s.capabilities.Write.Idempotency
	switch key.tag {
	case writeKeySuffix:
		if !validWireToken(key.value) {
			return "", &InvalidKeyError{
				Key:    key.value,
				Reason: "suffix must use letters, digits, '.', '_', ':', or '-'",
			}
		}
		if prefix := idempotency.RequiredKeyPrefix; prefix != "" &&
			strings.HasPrefix(key.value, prefix) {
			return "", &InvalidKeyError{
				Key: key.value,
				Reason: fmt.Sprintf(
					"suffix already contains the advertised required prefix %q", prefix),
			}
		}
		resolved := idempotency.RequiredKeyPrefix + key.value
		if err := validateKeySyntax(
			resolved, idempotency.MaxKeyBytes, idempotency.RequiredKeyPrefix,
		); err != nil {
			return "", err
		}
		return resolved, nil
	case writeKeyFull:
		if err := validateKeySyntax(
			key.value, idempotency.MaxKeyBytes, idempotency.RequiredKeyPrefix,
		); err != nil {
			return "", err
		}
		return key.value, nil
	default:
		return "", &InvalidKeyError{Reason: "use joeydb.KeySuffix or joeydb.FullKey"}
	}
}

func (s *Session) validateWriteFeatures(features writepkg.Features) error {
	refuse := func(category, value string) error {
		return &CapabilityError{
			Reason: fmt.Sprintf("typed write requires advertised %s %q", category, value),
		}
	}
	for _, operation := range features.Operations {
		if !contains(s.capabilities.Write.Operations, string(operation)) {
			return refuse("operation", string(operation))
		}
	}
	for _, kind := range features.ObjectKinds {
		if !contains(s.capabilities.Write.ObjectKinds, string(kind)) {
			return refuse("object kind", string(kind))
		}
	}
	for _, form := range features.ExpirationForms {
		if !contains(s.capabilities.Write.ExpirationForms, string(form)) {
			return refuse("expiration form", string(form))
		}
	}
	for _, mode := range features.VocabularyModes {
		if !contains(s.capabilities.Write.VocabularyModes, string(mode)) {
			return refuse("vocabulary mode", string(mode))
		}
	}
	for _, mode := range features.RecordModes {
		name := mode.CapabilityName()
		if !contains(s.capabilities.Write.RecordModes, name) {
			return refuse("record mode", name)
		}
	}
	for _, selector := range features.RetractionSelectors {
		if !contains(s.capabilities.Write.RetractSelectors, string(selector)) {
			return refuse("retraction selector", string(selector))
		}
	}
	return nil
}
