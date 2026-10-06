package policy

import (
	"encoding/base64"
	"errors"
	"strings"
)

var ErrInvalidBucketKeyInput = errors.New("invalid bucket key input")

// buildKey returns the canonical Redis key for a policy's enforcement bucket.
//
// Policy version is intentionally excluded: changing policy configuration does
// not automatically create a fresh bucket. The scope determines which request
// dimensions distinguish buckets.
func buildKey(request Request, policy Policy) (string, error) {
	if policy.ID == "" || request.TenantID == "" {
		return "", ErrInvalidBucketKeyInput
	}

	parts := []string{
		"rl",
		"v1",
		string(policy.Scope),
		"t",
		encodeKeyComponent(request.TenantID),
		"p",
		encodeKeyComponent(policy.ID),
	}

	switch policy.Scope {
	case ScopeTenant:
		// One bucket per policy and tenant.

	case ScopeIdentity:
		if err := request.Identity.Validate(); err != nil {
			return "", errors.Join(ErrInvalidBucketKeyInput, err)
		}

		parts = append(parts,
			"it", encodeKeyComponent(string(request.Identity.Type)),
			"i", encodeKeyComponent(request.Identity.Value),
		)

	case ScopeResource:
		if err := request.Resource.Validate(); err != nil {
			return "", errors.Join(ErrInvalidBucketKeyInput, err)
		}

		parts = append(parts,
			"m", encodeKeyComponent(request.Resource.Method),
			"r", encodeKeyComponent(request.Resource.Path),
		)

	case ScopeIdentityResource:
		if err := request.Identity.Validate(); err != nil {
			return "", errors.Join(ErrInvalidBucketKeyInput, err)
		}
		if err := request.Resource.Validate(); err != nil {
			return "", errors.Join(ErrInvalidBucketKeyInput, err)
		}

		parts = append(parts,
			"it", encodeKeyComponent(string(request.Identity.Type)),
			"i", encodeKeyComponent(request.Identity.Value),
			"m", encodeKeyComponent(request.Resource.Method),
			"r", encodeKeyComponent(request.Resource.Path),
		)

	default:
		return "", ErrInvalidBucketKeyInput
	}

	return strings.Join(parts, ":"), nil
}

// Raw URL-safe Base64 avoids delimiter ambiguity and unsafe key characters.
func encodeKeyComponent(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
