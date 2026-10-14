package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsJSON(t *testing.T, settings AppSettings) string {
	t.Helper()
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPanelRejectsUnusableConfigWithoutSaving(t *testing.T) {
	for _, name := range []string{"missing-file", "directory", "missing-provider", "missing-url", "unexpected-route", "invalid-retries", "read-only"} {
		t.Run(name, func(t *testing.T) {
			_, d, server := dashboardFixture(t)
			settings := d.active
			if err := saveAppSettings(d.settingsFile, settings); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(d.settingsFile)
			switch name {
			case "missing-file":
				settings.CodexConfig = filepath.Join(t.TempDir(), "typo", "config.toml")
			case "directory":
				settings.CodexConfig = filepath.Join(t.TempDir(), "directory.toml")
				if err := os.Mkdir(settings.CodexConfig, 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing-provider":
				settings.Provider = "missing-provider"
			case "missing-url":
				if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unexpected-route":
				if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\nbase_url = 'https://external.example/v1'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "invalid-retries":
				if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\nbase_url = '"+settings.guardianBaseURL()+"'\nrequest_max_retries = 'bad'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "read-only":
				if err := os.Chmod(settings.CodexConfig, 0o400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(settings.CodexConfig, 0o600) })
			}
			configBefore, _ := os.ReadFile(settings.CodexConfig)
			resp := dashboardRequest(t, d, server, "settings", "PUT", settings, true, server.URL)
			if resp.StatusCode != 400 {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("unusable config accepted: %d %s", resp.StatusCode, body)
			}
			after, _ := os.ReadFile(d.settingsFile)
			configAfter, _ := os.ReadFile(settings.CodexConfig)
			if !bytes.Equal(before, after) || !bytes.Equal(configBefore, configAfter) {
				t.Fatal("invalid save changed settings or config")
			}
		})
	}
}

func TestPanelSavesUpstreamPortMigrationWithoutChangingActiveRoute(t *testing.T) {
	_, d, server := dashboardFixture(t)
	next := d.active
	next.Upstream = "http://127.0.0.1:17723"
	if strings.HasSuffix(next.Listen, ":17723") {
		next.Upstream = "http://127.0.0.1:17724"
	}
	before, _ := os.ReadFile(next.CodexConfig)
	resp := dashboardRequest(t, d, server, "settings", "PUT", next, true, server.URL)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("valid port migration refused: %d %s", resp.StatusCode, body)
	}
	saved, err := readAppSettings(d.settingsFile, defaultAppSettings())
	if err != nil || saved != next || d.active.Upstream == next.Upstream {
		t.Fatal("pending settings were not kept separate from the active route")
	}
	after, _ := os.ReadFile(next.CodexConfig)
	if !bytes.Equal(before, after) {
		t.Fatal("saving a restart-only change rewrote the active Codex route")
	}
}

func TestPanelDefaultHTTPPort(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
		for _, originPort := range []string{"", ":80"} {
			t.Run(host+originPort, func(t *testing.T) {
				g, d, _ := dashboardFixture(t)
				d.active.Listen = host + ":80"
				d.manager = NewConfigManager(d.active.CodexConfig, d.active.Provider, d.active.ccswitchBaseURL(), d.active.guardianBaseURL())
				if err := os.WriteFile(d.active.CodexConfig, []byte("[model_providers.custom]\nbase_url = '"+d.active.guardianBaseURL()+"'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{"panel", "state", "mode", "settings"} {
					method, body := "GET", ""
					if path == "mode" {
						method, body = "POST", `{"mode":"off"}`
					}
					if path == "settings" {
						method = "PUT"
						body = settingsJSON(t, d.active)
					}
					req := httptest.NewRequest(method, "http://"+host+"/__responses_guardian/"+path, strings.NewReader(body))
					req.RemoteAddr = "127.0.0.1:45678"
					req.Header.Set("Origin", "http://"+host+originPort)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Guardian-Token", d.token)
					resp := httptest.NewRecorder()
					d.ServeHTTP(resp, req)
					if resp.Code != 200 {
						t.Fatalf("default-port %s refused: %d %s", path, resp.Code, resp.Body.String())
					}
				}
				if g.currentMode() != recoveryModeOff {
					t.Fatal("authorized default-port mode request was not applied")
				}
			})
		}
	}
}

func TestPanelDefaultPortKeepsOriginBoundary(t *testing.T) {
	_, d, _ := dashboardFixture(t)
	d.active.Listen = "127.0.0.1:80"
	for _, tc := range []struct{ host, origin, remote, site string }{
		{"evil.example", "", "127.0.0.1:1234", ""},
		{"127.0.0.1:81", "", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://127.0.0.1:81", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://localhost", "127.0.0.1:1234", ""},
		{"127.0.0.1", "https://127.0.0.1", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://127.0.0.1/path", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://127.0.0.1?query", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://user@127.0.0.1", "127.0.0.1:1234", ""},
		{"127.0.0.1", "null", "127.0.0.1:1234", ""},
		{"127.0.0.1", "http://127.0.0.1", "192.0.2.5:1234", ""},
		{"127.0.0.1", "http://127.0.0.1", "127.0.0.1:1234", "cross-site"},
		{"127.0.0.1:", "", "127.0.0.1:1234", ""},
	} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/__responses_guardian/panel", nil)
		req.Host, req.RemoteAddr = tc.host, tc.remote
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		resp := httptest.NewRecorder()
		d.ServeHTTP(resp, req)
		if resp.Code != 403 {
			t.Fatalf("unsafe request accepted: %+v", tc)
		}
	}
}
