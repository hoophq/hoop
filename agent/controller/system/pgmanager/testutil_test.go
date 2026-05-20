package pgmanager

// buildTestSnapshot constructs a minimal Snapshot suitable for use in
// plan-level tests. It uses defaultAttributes() and empty collections so
// tests only need to override the fields they care about.
func buildTestSnapshot(role string, exists bool) *Snapshot {
	return &Snapshot{
		Role:        role,
		Exists:      exists,
		Attributes:  defaultAttributes(),
		Memberships: []string{},
		ScopeStates: map[string]Scope{},
	}
}
