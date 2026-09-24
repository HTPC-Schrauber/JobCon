package nexus

import (
	"strings"
	"testing"
)

func TestValidateNexusURL_Valid(t *testing.T) {
	validURLs := []string{
		"http://localhost:8081",
		"http://127.0.0.1:8081",
		"https://nexus.example.com",
		"http://nexus.intern:8081",
		"http://nexus.intern:8081/",
		"http://nexus.intern:8081/repository",
		"https://nexus.test.internal/repo",
		"https://nexus-1.dev-cluster.local:8443/nested/path/",
		"http://192.168.1.100:8081",
		"http://10.0.0.5:8080/nexus",
		"http://[::1]:8081",
	}

	for _, u := range validURLs {
		if !SafeNexusURLRegex.MatchString(u) {
			t.Errorf("expected SafeNexusURLRegex to match %q", u)
		}
		if err := ValidateNexusURL(u); err != nil {
			t.Errorf("expected ValidateNexusURL to succeed for %q, got: %v", u, err)
		}
	}
}

func TestValidateNexusURL_Invalid(t *testing.T) {
	tests := []struct {
		url     string
		errSub  string
	}{
		{"", "darf nicht leer sein"},
		{"   ", "darf nicht leer sein"},
		{"ftp://nexus.example.com", "nur HTTP- oder HTTPS-URLs"},
		{"file:///etc/passwd", "nur HTTP- oder HTTPS-URLs"},
		{"javascript:alert(1)", "nur HTTP- oder HTTPS-URLs"},
		{"gopher://nexus.example.com", "nur HTTP- oder HTTPS-URLs"},
		{"http://nexus.example.com?query=1", "nur HTTP- oder HTTPS-URLs"},
		{"http://nexus.example.com#fragment", "nur HTTP- oder HTTPS-URLs"},
		{"http://user:pass@nexus.example.com", "nur HTTP- oder HTTPS-URLs"},
		{"http://nexus.example.com/path with spaces", "nur HTTP- oder HTTPS-URLs"},
		{"http://nexus.example.com\r\nInjected: header", "nur HTTP- oder HTTPS-URLs"},
		{"http://169.254.169.254", "nicht gestattet"},
		{"http://169.254.169.254:8080/latest/meta-data", "nicht gestattet"},
		{"http://metadata.google.internal/computeMetadata/v1/", "nicht gestattet"},
		{"http://instance-data/latest/meta-data", "nicht gestattet"},
		{"http://[fe80::1]:8080", "nicht gestattet"},
	}

	for _, tc := range tests {
		err := ValidateNexusURL(tc.url)
		if err == nil {
			t.Errorf("expected error for %q, got nil", tc.url)
			continue
		}
		if !strings.Contains(err.Error(), tc.errSub) {
			t.Errorf("expected error message for %q to contain %q, got: %v", tc.url, tc.errSub, err)
		}
	}
}
