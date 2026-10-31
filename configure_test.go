package bb

import (
	"context"
	"os"
	"path/filepath"
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

func configEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestConfigureSetAndGet(t *testing.T) {
	configEnv(t)
	home := t.TempDir()

	for _, kv := range [][2]string{{"workspace", " my-team "}, {"email", "me@example.com"}, {"token", "s3cret"}, {"home", home}} {
		if _, err := runBBOut("configure", "set", kv[0], kv[1]); err != nil {
			t.Fatalf("set %s: %v", kv[0], err)
		}
	}

	for name, want := range map[string]string{"workspace": "my-team", "email": "me@example.com", "token": "********", "home": home} {
		out, err := runBBOut("configure", "get", name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if strings.TrimSpace(out) != want {
			t.Errorf("get %s = %q, want %q", name, out, want)
		}
	}

	cfg, err := loadConfig()
	if err != nil || cfg.Workspace != "my-team" || cfg.Token != "s3cret" || cfg.Home != home {
		t.Errorf("stored config = %+v, err %v", cfg, err)
	}
}

func TestConfigureGetUnsafeShowSecrets(t *testing.T) {
	configEnv(t)
	if err := runBB("configure", "set", "token", "s3cret"); err != nil {
		t.Fatal(err)
	}

	out, err := runBBOut("configure", "get", "--unsafe-show-secrets", "token")
	if err != nil || strings.TrimSpace(out) != "s3cret" {
		t.Errorf("get token with flag = %q, err %v", out, err)
	}
	out, err = runBBOut("configure", "get", "--unsafe-show-secrets")
	if err != nil || !strings.Contains(out, "token=s3cret\n") {
		t.Errorf("listing with flag = %q, err %v", out, err)
	}
	out, err = runBBOut("configure", "get", "token")
	if err != nil || strings.Contains(out, "s3cret") {
		t.Errorf("get token without flag leaked: %q, err %v", out, err)
	}
}

func TestConfigureGetAllMasksToken(t *testing.T) {
	configEnv(t)
	if err := runBB("configure", "set", "workspace", "my-team"); err != nil {
		t.Fatal(err)
	}
	if err := runBB("configure", "set", "token", "s3cret"); err != nil {
		t.Fatal(err)
	}

	out, err := runBBOut("configure", "get")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("listing leaked the token:\n%s", out)
	}
	for _, want := range []string{"workspace=my-team\n", "email=\n", "token=********\n", "home=\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
}

func TestConfigureSetDoesNotEchoToken(t *testing.T) {
	configEnv(t)
	out, err := runBBOut("configure", "set", "token", "s3cret")
	if err != nil || strings.Contains(out, "s3cret") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestConfigureGetUnsetFailsWithHint(t *testing.T) {
	configEnv(t)
	err := runBB("configure", "get", "home")
	if err == nil || !strings.Contains(err.Error(), "bb configure") {
		t.Errorf("want error with a `bb configure` hint, got %v", err)
	}
}

func TestConfigureSetErrors(t *testing.T) {
	configEnv(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := map[string][]string{
		"unknown name":     {"configure", "set", "bogus", "x"},
		"unknown get name": {"configure", "get", "bogus"},
		"missing value":    {"configure", "set", "workspace"},
		"too many args":    {"configure", "set", "workspace", "a", "b"},
		"empty required":   {"configure", "set", "email", "  "},
		"home is a file":   {"configure", "set", "home", file},
		"get too many":     {"configure", "get", "email", "token"},
	}
	for name, args := range tests {
		if err := runBB(args...); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := runBB("configure", "set", "bogus", "x"); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("unknown-name error should list valid options, got %v", err)
	}
}

func TestConfigureSetHomeNormalizesAndClears(t *testing.T) {
	configEnv(t)
	t.Setenv("HOME", t.TempDir())

	if err := runBB("configure", "set", "home", "~/code"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !filepath.IsAbs(cfg.Home) || strings.Contains(cfg.Home, "~") {
		t.Errorf("home not normalized: %q", cfg.Home)
	}

	if err := runBB("configure", "set", "home", ""); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(); cfg.Home != "" {
		t.Errorf("home should be cleared, got %q", cfg.Home)
	}
}

func TestCompleteConfigNames(t *testing.T) {
	configEnv(t)
	var buf strings.Builder
	app := NewApp()
	app.Writer = &buf
	if err := app.Run(context.Background(), []string{"bb", "configure", "get", "--generate-shell-completion"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range configOptionNames() {
		if !strings.Contains(buf.String(), name) {
			t.Errorf("completion missing %q:\n%s", name, buf.String())
		}
	}
}
