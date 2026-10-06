package policy

import "testing"

func TestAppliesTo(t *testing.T) {
	request := Request{
		Identity: Identity{
			Type:  IdentityUser,
			Value: "user-123",
		},
		TenantID: "tenant-123",
		Resource: Resource{
			Method: "POST",
			Path:   "/users/{user_id}/orders",
		},
		Cost: 1,
	}

	tests := []struct {
		name   string
		policy Policy
		want   bool
	}{
		{
			name: "matching policy",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
				TenantID:     "tenant-123",
				Resource: Resource{
					Method: "POST",
					Path:   "/users/{user_id}/orders",
				},
			},
			want: true,
		},
		{
			name: "disabled policy",
			policy: Policy{
				Enabled:      false,
				IdentityType: IdentityUser,
			},
			want: false,
		},
		{
			name: "wrong tenant",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
				TenantID:     "tenant-999",
			},
			want: false,
		},
		{
			name: "wrong identity type",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityAPIKey,
			},
			want: false,
		},
		{
			name: "wrong method",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
				Resource: Resource{
					Method: "GET",
				},
			},
			want: false,
		},
		{
			name: "wrong resource",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
				Resource: Resource{
					Path: "/products",
				},
			},
			want: false,
		},
		{
			name: "tenant wildcard",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
			},
			want: true,
		},
		{
			name: "resource wildcard",
			policy: Policy{
				Enabled:      true,
				IdentityType: IdentityUser,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppliesTo(request, tt.policy)

			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
