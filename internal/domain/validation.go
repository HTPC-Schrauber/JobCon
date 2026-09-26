package domain

import (
	"errors"
	"regexp"
)

var (
	// SafeIdentifierRe is used to validate IDs, versions, and names to prevent injection.
	SafeIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ValidateIdentifier checks if a string is a safe identifier.
func ValidateIdentifier(id string, fieldName string) error {
	if id != "" && !SafeIdentifierRe.MatchString(id) {
		return errors.New("invalid " + fieldName + ": only alphanumeric characters, dots, underscores, and hyphens are allowed")
	}
	return nil
}
