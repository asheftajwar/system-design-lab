package policy

func AppliesTo(request Request, policy Policy) bool {
	if !policy.Enabled {
		return false
	}

	if policy.TenantID != "" && policy.TenantID != request.TenantID {
		return false
	}

	if policy.IdentityType != request.Identity.Type {
		return false
	}

	if policy.Resource.Method != "" &&
		policy.Resource.Method != request.Resource.Method {
		return false
	}

	if policy.Resource.Path != "" &&
		policy.Resource.Path != request.Resource.Path {
		return false
	}

	return true
}
