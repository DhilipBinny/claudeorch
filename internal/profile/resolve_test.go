package profile

import "testing"

func TestResolve_Matches(t *testing.T) {
	s := &Store{Profiles: map[string]*Profile{
		"work": {Name: "work", Email: "a@x.com", OrganizationUUID: "org-1"},
		"home": {Name: "home", Email: "b@y.com", OrganizationUUID: "org-2"},
	}}
	name, ok := Resolve(s, "a@x.com", "org-1")
	if !ok {
		t.Fatal("expected match for a@x.com/org-1")
	}
	if name != "work" {
		t.Errorf("got %q, want work", name)
	}
}

func TestResolve_NoMatch(t *testing.T) {
	s := &Store{Profiles: map[string]*Profile{
		"work": {Name: "work", Email: "a@x.com", OrganizationUUID: "org-1"},
	}}
	if _, ok := Resolve(s, "a@x.com", "different-org"); ok {
		t.Error("should not match when org differs")
	}
	if _, ok := Resolve(s, "other@x.com", "org-1"); ok {
		t.Error("should not match when email differs")
	}
}

func TestResolve_EmptyStore(t *testing.T) {
	s := &Store{Profiles: map[string]*Profile{}}
	if _, ok := Resolve(s, "anything", "anywhere"); ok {
		t.Error("empty store should never match")
	}
}

func TestResolve_SameEmailDifferentOrg(t *testing.T) {
	// Same person, two different orgs — must be distinct profiles.
	s := &Store{Profiles: map[string]*Profile{
		"work": {Name: "work", Email: "me@x.com", OrganizationUUID: "org-A"},
		"home": {Name: "home", Email: "me@x.com", OrganizationUUID: "org-B"},
	}}
	nameA, okA := Resolve(s, "me@x.com", "org-A")
	nameB, okB := Resolve(s, "me@x.com", "org-B")
	if !okA || !okB {
		t.Fatal("both lookups should match")
	}
	if nameA == nameB {
		t.Errorf("distinct orgs returned same profile: %s vs %s", nameA, nameB)
	}
}
