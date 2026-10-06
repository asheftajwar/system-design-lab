package policy

import (
	"errors"
	"testing"
)

func TestPolicyValidate(t *testing.T) {
	validPolicy := Policy{
		ID:              "free-api",
		Version:         1,
		Name:            "Free API",
		Algorithm:       AlgorithmTokenBucket,
		Limit:           100,
		RefillRate:      10,
		RequestCost:     1,
		IdentityType:    IdentityAPIKey,
		Scope:           "tenant",
		EnforcementMode: EnforcementStrict,
		Enabled:         true,
	}

	tests := []struct {
		name    string
		mutate  func(*Policy)
		wantErr error
	}{
		{
			name:    "valid policy",
			mutate:  func(p *Policy) {},
			wantErr: nil,
		},
		{
			name:    "missing id",
			mutate:  func(p *Policy) { p.ID = "" },
			wantErr: ErrInvalidPolicyID,
		},
		{
			name:    "invalid version",
			mutate:  func(p *Policy) { p.Version = 0 },
			wantErr: ErrInvalidPolicyVersion,
		},
		{
			name:    "missing algorithm",
			mutate:  func(p *Policy) { p.Algorithm = "" },
			wantErr: ErrInvalidAlgorithm,
		},
		{
			name:    "invalid limit",
			mutate:  func(p *Policy) { p.Limit = 0 },
			wantErr: ErrInvalidLimit,
		},
		{
			name:    "invalid refill rate",
			mutate:  func(p *Policy) { p.RefillRate = 0 },
			wantErr: ErrInvalidRefillRate,
		},
		{
			name:    "invalid request cost",
			mutate:  func(p *Policy) { p.RequestCost = 0 },
			wantErr: ErrInvalidRequestCost,
		},
		{
			name:    "missing identity type",
			mutate:  func(p *Policy) { p.IdentityType = "" },
			wantErr: ErrInvalidIdentityType,
		},
		{
			name:    "missing scope",
			mutate:  func(p *Policy) { p.Scope = "" },
			wantErr: ErrInvalidPolicyScope,
		},
		{
			name:    "missing enforcement mode",
			mutate:  func(p *Policy) { p.EnforcementMode = "" },
			wantErr: ErrInvalidEnforcement,
		},
		{
			name: "token bucket rejects window",
			mutate: func(p *Policy) {
				p.WindowSeconds = 60
			},
			wantErr: ErrInvalidWindow,
		},
		{
			name: "fixed window requires window",
			mutate: func(p *Policy) {
				p.Algorithm = AlgorithmFixedWindow
				p.RefillRate = 0
				p.WindowSeconds = 60
			},
			wantErr: nil,
		},
		{
			name: "fixed window rejects refill rate",
			mutate: func(p *Policy) {
				p.Algorithm = AlgorithmFixedWindow
				p.RefillRate = 10
				p.WindowSeconds = 60
			},
			wantErr: ErrInvalidRefillRate,
		},
		{
			name: "sliding window requires window",
			mutate: func(p *Policy) {
				p.Algorithm = AlgorithmSlidingWindow
				p.RefillRate = 0
				p.WindowSeconds = 60
			},
			wantErr: nil,
		},
		{
			name: "unknown algorithm",
			mutate: func(p *Policy) {
				p.Algorithm = Algorithm("unknown")
			},
			wantErr: ErrInvalidAlgorithm,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPolicy
			tt.mutate(&p)

			err := p.Validate()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
