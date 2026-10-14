package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestOffPassesExactBodiesEncodingsAndErrors(t *testing.T) {
	raw := []byte(`{"previous_response_id":"keep","input":[{"type":"reasoning","encrypted_content":"keep-this"}]}`)
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(raw, nil)
	encoder.Close()
	cases := []struct {
		name     string
		path     string
		body     []byte
		encoding string
	}{
		{"plain", "/v1/responses?test=a%2Fb", raw, ""},
		{"zstd", "/v1/responses", compressed, "zstd"},
		{"unknown-encoding", "/v1/responses/compact", []byte("unparsed-body-larger-than-the-buffer-limit"), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			responseBody := []byte(`{"error":{"code":"invalid_encrypted_content","message":"pass this error through unchanged"}}`)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				got, readErr := io.ReadAll(r.Body)
				if readErr != nil || !bytes.Equal(got, tc.body) {
					t.Error("off mode altered the request body")
				}
				if r.RequestURI != tc.path || r.Header.Get("Content-Encoding") != tc.encoding || r.Header.Get("Authorization") != "Bearer local-test-only" {
					t.Error("request path, encoding or authorization changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream-Test", "preserved")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(responseBody)
			}))
			defer upstream.Close()
			upstreamURL, _ := url.Parse(upstream.URL)
			// A tiny sanitizer limit must not cap or truncate off-mode traffic.
			g := NewGuardian(upstreamURL, 16, recoveryModeOff)
			server := httptest.NewServer(g.Handler())
			defer server.Close()
			req, _ := http.NewRequest(http.MethodPost, server.URL+tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Encoding", tc.encoding)
			req.Header.Set("Authorization", "Bearer local-test-only")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 400 || !bytes.Equal(got, responseBody) {
				t.Fatal("off mode changed or truncated the error response")
			}
			if resp.Header.Get("X-Upstream-Test") != "preserved" || resp.Header.Get("X-Responses-Guardian-Recovery") != "" || resp.Header.Get("X-Responses-Guardian-Sanitization") != "" {
				t.Fatal("off mode changed recovery/response headers")
			}
			if calls.Load() != 1 || g.recoveryAttempts.Load() != 0 || g.proactiveChecks.Load() != 0 || g.passthroughRequests.Load() != 1 {
				t.Fatal("off mode invoked the sanitizer or retried")
			}
		})
	}
}

func writeModeFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForMode(t *testing.T, g *Guardian, expected recoveryMode) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for g.currentMode() != expected {
		if time.Now().After(deadline) {
			t.Fatalf("mode = %q, want %q", g.currentMode(), expected)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func watchModeForTest(t *testing.T, g *Guardian) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.watchModeFile(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func TestModeFileSwitchesAllModesOnOneServerAndPersists(t *testing.T) {
	modePath := filepath.Join(t.TempDir(), "mode.json")
	writeModeFixture(t, modePath, `{"mode":"off"}`)
	mode, err := initialMode(recoveryModeOnError, modePath)
	if err != nil || mode != recoveryModeOff {
		t.Fatalf("saved startup mode = %q, err = %v", mode, err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("encrypted_content")) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_encrypted_content"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	g := NewGuardian(upstreamURL, 1<<20, mode)
	g.modeFile = modePath
	watchModeForTest(t, g)
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	payload := `{"input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"hello"}]}`
	for _, step := range []struct {
		mode       recoveryMode
		wantStatus int
		wantCalls  int32
	}{
		{recoveryModeOff, 400, 1},
		{recoveryModeAlways, 200, 2},
		{recoveryModeOnError, 200, 4},
	} {
		writeModeFixture(t, modePath, `{"mode":"`+string(step.mode)+`"}`)
		waitForMode(t, g, step.mode)
		resp, requestErr := http.Post(server.URL+"/v1/responses", "application/json", strings.NewReader(payload))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != step.wantStatus || calls.Load() != step.wantCalls {
			t.Fatalf("%s: status=%d calls=%d", step.mode, resp.StatusCode, calls.Load())
		}
	}
	resp, err := http.Get(server.URL + "/__responses_guardian/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var health struct {
		PID        int          `json:"pid"`
		Mode       recoveryMode `json:"mode"`
		Switchable bool         `json:"mode_switch_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.PID != os.Getpid() || health.Mode != recoveryModeOnError || !health.Switchable {
		t.Fatalf("unexpected health: %+v", health)
	}
	restored, err := initialMode(recoveryModeAlways, modePath)
	if err != nil || restored != recoveryModeOnError {
		t.Fatal("saved selection was not restored on a new startup")
	}
}

func TestLiveSwitchLeavesInFlightStreamIntact(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Query().Get("hold") == "1" {
			if bytes.Contains(body, []byte("encrypted_content")) {
				t.Error("always-mode stream was not sanitized at request start")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
				_, _ = io.WriteString(w, "data: last\n\n")
			case <-r.Context().Done():
				t.Error("in-flight stream was cancelled during mode switch")
			}
			return
		}
		if !bytes.Contains(body, []byte("encrypted_content")) {
			t.Error("new off-mode request did not preserve opaque history")
		}
		_, _ = io.WriteString(w, "new-request-ok")
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	g := NewGuardian(upstreamURL, 1<<20, recoveryModeAlways)
	server := httptest.NewServer(g.Handler())
	defer server.Close()
	defer finish()
	client := &http.Client{Timeout: 4 * time.Second}
	payload := `{"input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"hello"}]}`
	stream, err := client.Post(server.URL+"/v1/responses?hold=1", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: first\n" {
		t.Fatalf("stream did not arrive incrementally: %q %v", first, err)
	}
	g.setMode(recoveryModeOff)
	next, err := client.Post(server.URL+"/v1/responses", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, next.Body)
	_ = next.Body.Close()
	finish()
	remaining, err := io.ReadAll(reader)
	if err != nil || string(remaining) != "\ndata: last\n\n" {
		t.Fatalf("stream was interrupted: %q %v", remaining, err)
	}
	if stream.Header.Get("X-Responses-Guardian-Sanitization") != "proactive" {
		t.Fatal("in-flight request did not retain its original mode")
	}
}

func TestModeFileRejectsInvalidPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.json")
	for _, input := range []string{
		`{"mode":"invalid"}`,
		`{"mode":"off"} {"mode":"always"}`,
		`{"mode":"off","extra":true}`,
		`null`,
		strings.Repeat(" ", 4097),
	} {
		writeModeFixture(t, path, input)
		if _, err := readModeFile(path); err == nil {
			t.Fatal("invalid preference was accepted")
		}
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	if got, err := initialMode(recoveryModeOnError, missing); err != nil || got != recoveryModeOnError {
		t.Fatal("missing preference must preserve the startup default")
	}
}
