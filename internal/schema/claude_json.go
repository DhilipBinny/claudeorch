package schema

import (
	"encoding/json"
	"fmt"
)

// Identity holds the account identity fields extracted from .claude.json.
// This is what claudeorch uses to build a Profile after `claudeorch add`.
type Identity struct {
	EmailAddress     string // oauthAccount.emailAddress
	OrganizationUUID string // oauthAccount.organizationUuid
	OrganizationName string // oauthAccount.organizationName (may be empty)
}

// ExtractIdentity reads identity fields from a .claude.json blob.
//
// It walks oauthAccount defensively — missing organizationName is allowed
// (personal accounts may not have one), but missing emailAddress or
// organizationUuid causes ErrSchemaIncompatible.
//
// The blob must be ≤ maxFileSize.
func ExtractIdentity(data []byte) (*Identity, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty .claude.json", ErrSchemaIncompatible)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("schema: .claude.json is %d bytes, exceeds max %d", len(data), maxFileSize)
	}

	var envelope struct {
		OAuthAccount *struct {
			EmailAddress     string `json:"emailAddress"`
			OrganizationUUID string `json:"organizationUuid"`
			OrganizationName string `json:"organizationName"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("schema: .claude.json parse error: %w", err)
	}
	if envelope.OAuthAccount == nil {
		return nil, fmt.Errorf("%w: missing \"oauthAccount\" key (not logged in?)", ErrSchemaIncompatible)
	}
	oa := envelope.OAuthAccount
	if oa.EmailAddress == "" {
		return nil, fmt.Errorf("%w: oauthAccount.emailAddress is empty", ErrSchemaIncompatible)
	}
	if oa.OrganizationUUID == "" {
		return nil, fmt.Errorf("%w: oauthAccount.organizationUuid is empty", ErrSchemaIncompatible)
	}

	return &Identity{
		EmailAddress:     oa.EmailAddress,
		OrganizationUUID: oa.OrganizationUUID,
		OrganizationName: oa.OrganizationName,
	}, nil
}
