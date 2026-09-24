package runner

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	validIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ShellQuote returns a safely quoted string for POSIX shells using single quotes.
// In POSIX shells (bash, dash, sh), single-quoted strings cannot contain variable expansions,
// command substitutions, or escape interpretation.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ValidateVersion checks that a job version or artifact ID contains only safe identifier characters.
func ValidateVersion(v string) error {
	if v == "" {
		return errors.New("version darf nicht leer sein")
	}
	if !validIdentifierRegex.MatchString(v) {
		return fmt.Errorf("ungültiges Versionsformat: %q (nur alphanumerische Zeichen, '.', '-', '_' erlaubt)", v)
	}
	return nil
}

// ValidateContext checks that a Talend context name contains only safe identifier characters.
func ValidateContext(c string) error {
	if c == "" {
		return nil
	}
	if !validIdentifierRegex.MatchString(c) {
		return fmt.Errorf("ungültiger Kontextname: %q (nur alphanumerische Zeichen, '.', '-', '_' erlaubt)", c)
	}
	return nil
}

// ValidateParamKey checks that a parameter key contains only safe identifier characters.
func ValidateParamKey(k string) error {
	if k == "" {
		return errors.New("parameter-Schlüssel darf nicht leer sein")
	}
	if !validIdentifierRegex.MatchString(k) {
		return fmt.Errorf("ungültiger Parameter-Schlüssel: %q (nur alphanumerische Zeichen, '.', '-', '_' erlaubt)", k)
	}
	return nil
}

// ValidateParamValue checks that a parameter value does not contain null bytes.
func ValidateParamValue(v string) error {
	if strings.ContainsRune(v, 0) {
		return errors.New("parameter-Wert darf keine Null-Bytes enthalten")
	}
	return nil
}

// ValidateEnvFile checks that the environment file path does not contain shell injection characters.
func ValidateEnvFile(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsAny(p, "\n\r;`$&|*?<>~") {
		return fmt.Errorf("ungültiger Pfad für Environment-Datei: %q (enthält unerlaubte Zeichen)", p)
	}
	return nil
}

// ValidatePath checks that directory paths do not contain shell injection characters.
func ValidatePath(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsAny(p, "\n\r;`$&|*?<>~") {
		return fmt.Errorf("ungültiger Pfad: %q (enthält unerlaubte Zeichen)", p)
	}
	return nil
}
