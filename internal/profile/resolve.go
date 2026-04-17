package profile

// Resolve searches the store for an existing profile matching the given email
// and organization UUID. Returns the profile name and true if found.
//
// The combination of (email, orgUUID) is the uniqueness key — the same email
// can appear in multiple orgs, and the same org may have multiple members.
func Resolve(s *Store, email, orgUUID string) (string, bool) {
	for name, p := range s.Profiles {
		if p.Email == email && p.OrganizationUUID == orgUUID {
			return name, true
		}
	}
	return "", false
}
