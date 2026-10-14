package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRewriteProviderBaseURLOnlyTouchesTarget(t *testing.T) {
	input := []byte(`model_provider = "custom"

[model_providers.other]
base_url = "https://example.com/v1"

[model_providers.custom]
wire_api = "responses"
experimental_bearer_token = "keep-this-secret"
base_url = "http://127.0.0.1:15721/v1" # managed by CCSwitch
`)

	updated, changed, err := rewriteProviderBaseURL(
		input,
		"custom",
		"http://127.0.0.1:15721/v1",
		"http://127.0.0.1:15722/v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected config to change")
	}
	text := string(updated)
	if !strings.Contains(text, `base_url = "http://127.0.0.1:15722/v1" # managed by CCSwitch`) {
		t.Fatalf("target base_url not updated: %s", text)
	}
	if !strings.Contains(text, `base_url = "https://example.com/v1"`) {
		t.Fatal("other provider changed")
	}
	if !strings.Contains(text, `experimental_bearer_token = "keep-this-secret"`) {
		t.Fatal("credential line changed")
	}
}

func TestRewriteProviderBaseURLRefusesUnexpectedValue(t *testing.T) {
	input := []byte("[model_providers.custom]\nbase_url = \"https://unexpected.example/v1\"\n")
	_, _, err := rewriteProviderBaseURL(
		input,
		"custom",
		"http://127.0.0.1:15721/v1",
		"http://127.0.0.1:15722/v1",
	)
	if err == nil {
		t.Fatal("expected refusal for unexpected base_url")
	}
}

func TestRewriteProviderBaseURLAcceptsQuotedSection(t *testing.T) {
	input := []byte("[model_providers.\"custom\"]\r\nbase_url = 'http://127.0.0.1:15721/v1'\r\n")
	updated, changed, err := rewriteProviderBaseURL(
		input,
		"custom",
		"http://127.0.0.1:15721/v1",
		"http://127.0.0.1:15722/v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.Contains(string(updated), "base_url = 'http://127.0.0.1:15722/v1'\r\n") {
		t.Fatalf("quoted section rewrite failed: %q", string(updated))
	}
}

func TestConfigManagerWatchRepairsCCSwitchRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	ccswitch := "http://127.0.0.1:15721/v1"
	guardian := "http://127.0.0.1:15722/v1"
	initial := "[model_providers.custom]\nbase_url = \"" + ccswitch + "\"\nmodel = \"first\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewConfigManager(path, "custom", ccswitch, guardian)
	if changed, err := manager.EnsureGuardian(); err != nil || !changed {
		t.Fatalf("initial ensure: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(path + ".responses-guardian.bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Watch(ctx, 10*time.Millisecond)
	}()

	rewritten := "[model_providers.custom]\nbase_url = \"" + ccswitch + "\"\nmodel = \"second\"\n"
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, guardian) {
			if !strings.Contains(text, `model = "second"`) {
				t.Fatalf("watcher overwrote unrelated change: %s", text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not restore guardian URL: %s", text)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestConfigManagerEnsuresRetrySettingsAndPreservesHigherValues(t *testing.T) {
	input := []byte(`[model_providers.custom]
base_url = "http://127.0.0.1:15722/v1"
model = "gpt-test"
`)
	updated, changed, err := ensureProviderRetrySettings(input, "custom")
	if err != nil || !changed {
		t.Fatalf("ensure missing retry fields: changed=%v err=%v", changed, err)
	}
	text := string(updated)
	if !strings.Contains(text, "request_max_retries = 20") || !strings.Contains(text, "stream_max_retries = 20") {
		t.Fatalf("retry fields missing: %s", text)
	}

	higher := []byte(`[model_providers.custom]
base_url = "http://127.0.0.1:15722/v1"
request_max_retries = 50 # user choice
stream_max_retries = 30
`)
	preserved, changed, err := ensureProviderRetrySettings(higher, "custom")
	if err != nil || changed {
		t.Fatalf("higher retry values should be preserved: changed=%v err=%v", changed, err)
	}
	if string(preserved) != string(higher) {
		t.Fatal("higher retry settings were rewritten")
	}
}

func TestConfigManagerWatcherRestoresRetryFieldsAfterProviderRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := `[model_providers.custom]
base_url = "http://127.0.0.1:15721/v1"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewConfigManager(path, "custom", "http://127.0.0.1:15721/v1", "http://127.0.0.1:15722/v1")
	if changed, err := manager.EnsureGuardian(); err != nil || !changed {
		t.Fatalf("initial ensure: changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "request_max_retries = 20") {
		t.Fatal("initial ensure did not add retry policy")
	}
	rewritten := `[model_providers.custom]
base_url = "http://127.0.0.1:15721/v1"
model = "switched"
`
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.EnsureGuardian(); err != nil || !changed {
		t.Fatalf("repair after provider rewrite: changed=%v err=%v", changed, err)
	}
	data, _ = os.ReadFile(path)
	text := string(data)
	for _, expected := range []string{
		`base_url = "http://127.0.0.1:15722/v1"`,
		"request_max_retries = 20",
		"stream_max_retries = 20",
		`model = "switched"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q after repair: %s", expected, text)
		}
	}
}
