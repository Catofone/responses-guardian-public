package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dashboardFixture(t *testing.T) (*Guardian, *Dashboard, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"status":"healthy"}`) }))
	t.Cleanup(upstream.Close)
	upstreamURL, _ := url.Parse(upstream.URL)
	dir := t.TempDir()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	settings := defaultAppSettings()
	settings.Listen = server.Listener.Addr().String()
	settings.Upstream = upstream.URL
	settings.CodexConfig = filepath.Join(dir, "config.toml")
	settings.RequestRetries, settings.StreamRetries = 20, 20
	if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\nbase_url = \""+settings.guardianBaseURL()+"\"\nrequest_max_retries = 20\nstream_max_retries = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	g := NewGuardian(upstreamURL, 1<<20, recoveryModeOnError)
	g.modeFile = filepath.Join(dir, "mode.json")
	dashboard, err := NewDashboard(g, settings, filepath.Join(dir, "settings.json"), manager)
	if err != nil {
		t.Fatal(err)
	}
	g.dashboard = dashboard
	server.Config.Handler = g.Handler()
	server.Start()
	return g, dashboard, server
}

func dashboardRequest(t *testing.T, d *Dashboard, server *httptest.Server, path, method string, body any, auth bool, origin string) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, server.URL+"/__responses_guardian/"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("X-Guardian-Token", d.token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestPanelModePersistsAndPreservesProcess(t *testing.T) {
	g, d, server := dashboardFixture(t)
	resp := dashboardRequest(t, d, server, "mode", "POST", map[string]string{"mode": "off"}, true, server.URL)
	if resp.StatusCode != 200 || g.currentMode() != recoveryModeOff {
		t.Fatalf("mode update failed: %d", resp.StatusCode)
	}
	mode, err := readModeFile(g.modeFile)
	if err != nil || mode != recoveryModeOff {
		t.Fatal("mode update was not persisted")
	}
	page := dashboardRequest(t, d, server, "panel", "GET", nil, false, "")
	body, _ := io.ReadAll(page.Body)
	if page.StatusCode != 200 || !bytes.Contains(body, []byte("本地控制")) || bytes.Contains(body, []byte("__GUARDIAN_TOKEN__")) {
		t.Fatal("panel not rendered")
	}
	if !strings.Contains(page.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("panel isolation headers missing")
	}
}

func TestPanelRejectsCrossOriginAndMissingToken(t *testing.T) {
	g, d, server := dashboardFixture(t)
	for _, tc := range []struct {
		auth   bool
		origin string
	}{{false, ""}, {true, "https://evil.example"}, {true, "null"}} {
		resp := dashboardRequest(t, d, server, "mode", "POST", map[string]string{"mode": "always"}, tc.auth, tc.origin)
		if resp.StatusCode != 403 || g.currentMode() != recoveryModeOnError {
			t.Fatal("unauthorized mode change accepted")
		}
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://evil.example/__responses_guardian/panel", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	d.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("arbitrary Host accepted")
	}
}

func TestPanelRetryValuesCanDecreaseAndZero(t *testing.T) {
	_, d, server := dashboardFixture(t)
	settings := d.active
	// Use the panel's currently active route; config starts on that same route.
	if err := os.WriteFile(settings.CodexConfig, []byte("[model_providers.custom]\nbase_url = \""+settings.guardianBaseURL()+"\"\nrequest_max_retries = 20\nstream_max_retries = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.manager = NewConfigManager(settings.CodexConfig, settings.Provider, settings.ccswitchBaseURL(), settings.guardianBaseURL())
	settings.RequestRetries, settings.StreamRetries = 3, 0
	resp := dashboardRequest(t, d, server, "settings", "PUT", settings, true, server.URL)
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("save failed: %s", data)
	}
	data, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Contains(data, []byte("request_max_retries = 3")) || !bytes.Contains(data, []byte("stream_max_retries = 0")) {
		t.Fatalf("retry values not applied: %s", data)
	}
	saved, err := readAppSettings(d.settingsFile, defaultAppSettings())
	if err != nil || saved.RequestRetries != 3 || saved.StreamRetries != 0 {
		t.Fatal("retry values not persisted")
	}
	changed := settings
	changed.Listen = "127.0.0.1:17998"
	changed.Provider = "next-provider"
	changed.RequestRetries = 40
	// A pending provider must already exist in the selected config.
	data = append(data, []byte("\n[model_providers.next-provider]\nbase_url = \""+changed.ccswitchBaseURL()+"\"\n")...)
	if err := os.WriteFile(settings.CodexConfig, data, 0o600); err != nil {
		t.Fatal(err)
	}
	resp = dashboardRequest(t, d, server, "settings", "PUT", changed, true, server.URL)
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 || result["guardian_restart_required"] != true || result["retry_config_written"] != false {
		t.Fatalf("pending route incorrect: %v", result)
	}
	after, _ := os.ReadFile(settings.CodexConfig)
	if !bytes.Equal(after, data) {
		t.Fatal("pending route save overwrote current config")
	}
}

func TestPanelRejectsInvalidSettings(t *testing.T) {
	_, d, server := dashboardFixture(t)
	for _, change := range []func(*AppSettings){
		func(s *AppSettings) { s.Upstream = "https://external.example" },
		func(s *AppSettings) { s.Listen = "0.0.0.0:15722" },
		func(s *AppSettings) { s.RequestRetries = 101 },
		func(s *AppSettings) { s.Provider = "../../other" },
		func(s *AppSettings) { s.Listen = strings.TrimPrefix(s.Upstream, "http://") },
	} {
		s := d.active
		change(&s)
		resp := dashboardRequest(t, d, server, "settings", "PUT", s, true, server.URL)
		if resp.StatusCode != 400 {
			t.Fatalf("bad settings accepted: %v", s)
		}
	}
}

func TestAtomicConfigWriteRefusesChangedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("new user value"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := atomicWriteFileChecked(path, []byte("guardian value"), 0o600, []byte("old snapshot"), true)
	if err != errFileChanged {
		t.Fatalf("conflict not detected: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new user value" {
		t.Fatal("concurrent user edit lost")
	}
}

func TestGuardianDoesNotFollowUpstreamRedirect(t *testing.T) {
	redirectCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectCalls++; w.WriteHeader(200) }))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/responses", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	g := NewGuardian(upstreamURL, 1<<20, recoveryModeOnError)
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post(server.URL+"/v1/responses", "application/json", strings.NewReader(`{"input":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || redirectCalls != 0 {
		t.Fatal("Guardian followed a redirect away from its configured upstream")
	}
}
