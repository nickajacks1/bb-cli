package bb

import (
	"slices"
	"strings"
	"testing"
)

func TestNeedsTokenSetup(t *testing.T) {
	tests := []struct {
		token  string
		reauth bool
		want   bool
	}{
		{"", false, true},     // first run: walk through token creation
		{"tok", false, false}, // token already set: skip
		{"tok", true, true},   // --reauth: ask anyway
		{"", true, true},
	}
	for _, tc := range tests {
		if got := needsTokenSetup(&Config{Token: tc.token}, tc.reauth); got != tc.want {
			t.Errorf("needsTokenSetup(token=%q, reauth=%v) = %v, want %v", tc.token, tc.reauth, got, tc.want)
		}
	}
}

func TestTokenDescriptionMentionsReauthOnlyWhenKeeping(t *testing.T) {
	if got := tokenDescription(true); !strings.Contains(got, "--reauth") {
		t.Errorf("keeping-token hint should mention --reauth: %q", got)
	}
	if got := tokenDescription(false); strings.Contains(got, "--reauth") {
		t.Errorf("fresh-token text shouldn't mention --reauth: %q", got)
	}
}

func TestConfigureHasReauthFlag(t *testing.T) {
	for _, f := range configureCommand().Flags {
		if slices.Contains(f.Names(), "reauth") {
			return
		}
	}
	t.Error("configure should have a --reauth flag")
}
