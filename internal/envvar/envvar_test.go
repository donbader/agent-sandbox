package envvar

import (
	"testing"
)

func TestExtract(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Bearer ${TOKEN}", "TOKEN"},
		{"${API_KEY}", "API_KEY"},
		{"static-value", ""},
		{"no closing ${BRACE", ""},
		{"", ""},
		{"prefix ${MY_VAR} suffix", "MY_VAR"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := Extract(tc.input)
			if got != tc.want {
				t.Errorf("Extract(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("TEST_SECRET", "s3cret")

	tests := []struct {
		input string
		want  string
	}{
		{"${TEST_SECRET}", "s3cret"},
		{"Bearer ${TEST_SECRET}", "Bearer s3cret"},
		{"no-var", "no-var"},
		{"${UNSET_VAR}", ""},
		{"prefix ${TEST_SECRET} suffix", "prefix s3cret suffix"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := Expand(tc.input)
			if got != tc.want {
				t.Errorf("Expand(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		input       string
		wantEnvVar  string
		wantFormat  string
	}{
		{"Bearer ${TOKEN}", "TOKEN", "Bearer ${value}"},
		{"${API_KEY}", "API_KEY", "${value}"},
		{"static-value", "", ""},
		{"token ${SECRET} extra", "SECRET", "token ${value} extra"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			envVar, format := ParseTemplate(tc.input)
			if envVar != tc.wantEnvVar {
				t.Errorf("ParseTemplate(%q) envVar = %q, want %q", tc.input, envVar, tc.wantEnvVar)
			}
			if format != tc.wantFormat {
				t.Errorf("ParseTemplate(%q) format = %q, want %q", tc.input, format, tc.wantFormat)
			}
		})
	}
}
