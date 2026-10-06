package policy

import "errors"

type Algorithm string

const (
	AlgorithmTokenBucket   Algorithm = "token_bucket"
	AlgorithmFixedWindow   Algorithm = "fixed_window"
	AlgorithmSlidingWindow Algorithm = "sliding_window_log"
)

type EnforcementMode string

const (
	EnforcementStrict     EnforcementMode = "strict"
	EnforcementFailOpen   EnforcementMode = "fail_open"
	EnforcementFailClosed EnforcementMode = "fail_closed"
)

type IdentityType string

const (
	IdentityUser        IdentityType = "user"
	IdentityAPIKey      IdentityType = "api_key"
	IdentityApplication IdentityType = "application"
	IdentityTenant      IdentityType = "tenant"
	IdentityIP          IdentityType = "ip"
	IdentityService     IdentityType = "service"
	IdentityAnonymous   IdentityType = "anonymous"
)

type Identity struct {
	Type  IdentityType
	Value string
}

type Resource struct {
	Method string
	Path   string
}

type Scope string

const (
	ScopeTenant           Scope = "tenant"
	ScopeIdentity         Scope = "identity"
	ScopeResource         Scope = "resource"
	ScopeIdentityResource Scope = "identity_resource"
)

type Policy struct {
	ID            string
	Version       int64
	Name          string
	Algorithm     Algorithm
	Limit         int64
	RefillRate    float64
	WindowSeconds int64
	RequestCost   int64

	IdentityType IdentityType
	TenantID     string
	Resource     Resource

	Priority int

	Scope           Scope
	EnforcementMode EnforcementMode
	Enabled         bool
}

var (
	ErrInvalidPolicyID      = errors.New("policy id is required")
	ErrInvalidPolicyVersion = errors.New("policy version must be greater than zero")
	ErrInvalidLimit         = errors.New("limit must be greater than zero")
	ErrInvalidRefillRate    = errors.New("refill rate must be greater than zero")
	ErrInvalidWindow        = errors.New("window must be greater than zero")
	ErrInvalidRequestCost   = errors.New("request cost must be greater than zero")
	ErrInvalidIdentityType  = errors.New("identity type is required")
	ErrInvalidIdentityValue = errors.New("identity value is required")
	ErrInvalidAlgorithm     = errors.New("algorithm is required")
	ErrInvalidEnforcement   = errors.New("enforcement mode is required")
	ErrInvalidPolicyScope   = errors.New("invalid policy scope")
	ErrInvalidTenantID      = errors.New("tenant id is required")
)

func (r Resource) Key() string {
	return r.Method + ":" + r.Path
}

func (r Resource) Validate() error {
	if r.Method == "" {
		return errors.New("resource method is required")
	}

	if r.Path == "" {
		return errors.New("resource path is required")
	}

	return nil
}

func (i Identity) Validate() error {
	if i.Type == "" {
		return ErrInvalidIdentityType
	}

	if i.Value == "" {
		return ErrInvalidIdentityValue
	}

	return nil
}

func (p Policy) Validate() error {
	if p.ID == "" {
		return ErrInvalidPolicyID
	}

	if p.Version <= 0 {
		return ErrInvalidPolicyVersion
	}

	if p.Algorithm == "" {
		return ErrInvalidAlgorithm
	}

	if p.RequestCost <= 0 {
		return ErrInvalidRequestCost
	}

	if p.IdentityType == "" {
		return ErrInvalidIdentityType
	}

	if p.Scope == "" {
		return ErrInvalidPolicyScope
	}

	if p.EnforcementMode == "" {
		return ErrInvalidEnforcement
	}

	switch p.Scope {
	case ScopeTenant, ScopeIdentity, ScopeResource, ScopeIdentityResource:
		// valid
	default:
		return ErrInvalidPolicyScope
	}

	switch p.Algorithm {
	case AlgorithmTokenBucket:
		if p.Limit <= 0 {
			return ErrInvalidLimit
		}

		if p.RefillRate <= 0 {
			return ErrInvalidRefillRate
		}

		if p.WindowSeconds != 0 {
			return ErrInvalidWindow
		}

	case AlgorithmFixedWindow, AlgorithmSlidingWindow:
		if p.Limit <= 0 {
			return ErrInvalidLimit
		}

		if p.WindowSeconds <= 0 {
			return ErrInvalidWindow
		}

		if p.RefillRate != 0 {
			return ErrInvalidRefillRate
		}

	default:
		return ErrInvalidAlgorithm
	}

	return nil
}
