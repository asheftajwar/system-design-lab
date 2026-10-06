package policy

import (
	"strings"
	"testing"
)

func TestBuildKeyScopeSemantics(t *testing.T) {
	request := validRequest()
	policy := validPolicy("policy-a")

	build := func(r Request, p Policy) string {
		t.Helper()
		key, err := buildKey(r, p)
		if err != nil {
			t.Fatalf("buildKey() error = %v", err)
		}
		return key
	}

	t.Run("tenant scope ignores identity and resource", func(t *testing.T) {
		policy.Scope = ScopeTenant
		first := build(request, policy)

		other := request
		other.Identity.Value = "different-key"
		other.Resource.Path = "/orders"

		if got := build(other, policy); got != first {
			t.Fatalf("tenant scope should share a bucket: %q != %q", got, first)
		}
	})

	t.Run("identity scope separates identities but ignores resource", func(t *testing.T) {
		policy.Scope = ScopeIdentity
		first := build(request, policy)

		otherResource := request
		otherResource.Resource.Path = "/orders"
		if got := build(otherResource, policy); got != first {
			t.Fatal("identity scope should ignore resource")
		}

		otherIdentity := request
		otherIdentity.Identity.Value = "different-key"
		if got := build(otherIdentity, policy); got == first {
			t.Fatal("identity scope must separate identities")
		}
	})

	t.Run("resource scope separates resources but ignores identity", func(t *testing.T) {
		policy.Scope = ScopeResource
		first := build(request, policy)

		otherIdentity := request
		otherIdentity.Identity.Value = "different-key"
		if got := build(otherIdentity, policy); got != first {
			t.Fatal("resource scope should ignore identity")
		}

		otherResource := request
		otherResource.Resource.Path = "/orders"
		if got := build(otherResource, policy); got == first {
			t.Fatal("resource scope must separate resources")
		}
	})

	t.Run("identity resource scope includes both dimensions", func(t *testing.T) {
		policy.Scope = ScopeIdentityResource
		first := build(request, policy)

		otherIdentity := request
		otherIdentity.Identity.Value = "different-key"
		if got := build(otherIdentity, policy); got == first {
			t.Fatal("identity_resource scope must separate identities")
		}

		otherResource := request
		otherResource.Resource.Path = "/orders"
		if got := build(otherResource, policy); got == first {
			t.Fatal("identity_resource scope must separate resources")
		}
	})
}

func TestBuildKeyIsolation(t *testing.T) {
	request := validRequest()
	policy := validPolicy("policy-a")
	policy.Scope = ScopeTenant

	first, err := buildKey(request, policy)
	if err != nil {
		t.Fatal(err)
	}

	otherTenant := request
	otherTenant.TenantID = "tenant-other"
	tenantKey, err := buildKey(otherTenant, policy)
	if err != nil {
		t.Fatal(err)
	}
	if tenantKey == first {
		t.Fatal("different tenants must not share a bucket")
	}

	otherPolicy := policy
	otherPolicy.ID = "policy-b"
	policyKey, err := buildKey(request, otherPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if policyKey == first {
		t.Fatal("different policies must not share a bucket")
	}

	otherVersion := policy
	otherVersion.Version++
	versionKey, err := buildKey(request, otherVersion)
	if err != nil {
		t.Fatal(err)
	}
	if versionKey != first {
		t.Fatal("policy version should not implicitly reset bucket identity")
	}
}

func TestBuildKeyEncodesComponents(t *testing.T) {
	request := validRequest()
	request.TenantID = "tenant:one"
	policy := validPolicy("policy:one")
	policy.Scope = ScopeIdentity

	key, err := buildKey(request, policy)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(key, "tenant:one") || strings.Contains(key, "policy:one") {
		t.Fatalf("key contains unencoded components: %q", key)
	}
}

func TestBuildKeyRejectsInvalidInput(t *testing.T) {
	request := validRequest()
	policy := validPolicy("policy-a")

	tests := []struct {
		name    string
		request Request
		policy  Policy
	}{
		{
			name:    "missing tenant",
			request: Request{Identity: request.Identity, Resource: request.Resource},
			policy:  policy,
		},
		{
			name:    "missing policy id",
			request: request,
			policy:  Policy{Scope: ScopeTenant},
		},
		{
			name:    "invalid scope",
			request: request,
			policy:  Policy{ID: "policy-a", Scope: Scope("unknown")},
		},
		{
			name: "missing identity for identity scope",
			request: Request{
				TenantID: request.TenantID,
				Resource: request.Resource,
			},
			policy: Policy{ID: "policy-a", Scope: ScopeIdentity},
		},
		{
			name: "missing resource for resource scope",
			request: Request{
				TenantID: request.TenantID,
				Identity: request.Identity,
			},
			policy: Policy{ID: "policy-a", Scope: ScopeResource},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildKey(tt.request, tt.policy); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
