package aws

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAWSProfiles(t *testing.T) {
	cfg := `
[default]
region = eu-west-1

[profile core-auto/DeveloperAccess]
sso_session = foo

[profile core-audit/ReadOnlyAccess]

[sso-session foo]
sso_start_url = https://example.awsapps.com/start

[services my-services]
`
	got := parseAWSProfiles(strings.NewReader(cfg))
	want := []string{"default", "core-auto/DeveloperAccess", "core-audit/ReadOnlyAccess"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAWSProfiles = %v, want %v", got, want)
	}
}

func TestParseAWSProfiles_Empty(t *testing.T) {
	if got := parseAWSProfiles(strings.NewReader("# just a comment\n")); len(got) != 0 {
		t.Errorf("expected no profiles, got %v", got)
	}
}
