package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestGuardianRetriesOnlyEncryptedHistoryError(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if call == 1 {
			if !bytes.Contains(body, []byte("encrypted_content")) {
				t.Fatalf("first request was unexpectedly sanitized: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_encrypted_content","message":"Encrypted content could not be decrypted or parsed"}}`))
			return
		}

		if bytes.Contains(body, []byte("encrypted_content")) || bytes.Contains(body, []byte("previous_response_id")) {
			t.Fatalf("retry still contains upstream-bound state: %s", body)
		}
		for _, required := range []string{"call_1", "tool result", "summary survives"} {
			if !bytes.Contains(body, []byte(required)) {
				t.Fatalf("retry lost %q: %s", required, body)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeOnError).Handler())
	defer guardian.Close()

	payload := map[string]any{
		"previous_response_id": "resp_old",
		"stream":               true,
		"input": []any{
			map[string]any{"type": "reasoning", "encrypted_content": "gAAAA", "summary": []any{map[string]any{"type": "summary_text", "text": "summary survives"}}},
			map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "tool", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "tool result"},
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(guardian.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery"); got != "succeeded" {
		t.Fatalf("recovery header = %q", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	responseBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(responseBody), "response.completed") {
		t.Fatalf("stream response lost: %s", responseBody)
	}
}

func TestGuardianDoesNotRetryGenericBadRequest(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid model"}}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeOnError).Handler())
	defer guardian.Close()

	resp, err := http.Post(guardian.URL+"/v1/responses", "application/json", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestGuardianRetriesCCSwitchWrappedResourceMismatch(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if call == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"CC Switch local proxy failed. Provider: agentrouter; cause: OpenAI Responses bad request: The requested item was created under a different *** OpenAI resource. Use the same resource that created the item to access it.","code":"cc_switch_upstream_error","upstream_status":400}}`))
			return
		}
		for _, forbidden := range []string{"previous_response_id", "encrypted_content", `"id":"rs_old"`} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("retry still contains %q: %s", forbidden, body)
			}
		}
		for _, required := range []string{"call_1", "tool result", "resource summary"} {
			if !bytes.Contains(body, []byte(required)) {
				t.Fatalf("retry lost %q: %s", required, body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_recovered"}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeOnError).Handler())
	defer guardian.Close()

	payload := map[string]any{
		"previous_response_id": "resp_old",
		"input": []any{
			map[string]any{"type": "reasoning", "id": "rs_old", "encrypted_content": "opaque", "summary": []any{map[string]any{"type": "summary_text", "text": "resource summary"}}},
			map[string]any{"type": "function_call", "id": "fc_old", "call_id": "call_1", "name": "tool", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "id": "fco_old", "call_id": "call_1", "output": "tool result"},
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(guardian.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery"); got != "succeeded" {
		t.Fatalf("recovery header = %q", got)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery-Cause"); got != "resource-mismatch" {
		t.Fatalf("recovery cause = %q", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
}

func TestGuardianRecoversZstdCompressedRequest(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if call == 1 {
			if got := r.Header.Get("Content-Encoding"); got != "zstd" {
				t.Fatalf("first content-encoding = %q", got)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_encrypted_content"}}`))
			return
		}
		if got := r.Header.Get("Content-Encoding"); got != "" {
			t.Fatalf("retry content-encoding = %q, want empty", got)
		}
		if !json.Valid(body) || bytes.Contains(body, []byte("encrypted_content")) {
			t.Fatalf("retry body was not decoded and sanitized: %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_ok"}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeOnError).Handler())
	defer guardian.Close()

	raw := []byte(`{"previous_response_id":"resp_old","input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"continue"}]}`)
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(raw, nil)
	encoder.Close()

	req, _ := http.NewRequest(http.MethodPost, guardian.URL+"/v1/responses", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery"); got != "succeeded" {
		t.Fatalf("recovery header = %q", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
}

func TestGuardianPassesHealthThroughToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("upstream healthy"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeOnError).Handler())
	defer guardian.Close()

	resp, err := http.Get(guardian.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream healthy" {
		t.Fatalf("body = %q", body)
	}
}

func TestGuardianAlwaysModeSanitizesBeforeFirstForward(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"previous_response_id", "encrypted_content", `"id":"rs_old"`} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("first forward still contains %q: %s", forbidden, body)
			}
		}
		for _, required := range []string{"call_1", "tool result", "proactive summary"} {
			if !bytes.Contains(body, []byte(required)) {
				t.Fatalf("first forward lost %q: %s", required, body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_ok"}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeAlways).Handler())
	defer guardian.Close()

	payload := map[string]any{
		"previous_response_id": "resp_old",
		"input": []any{
			map[string]any{"type": "reasoning", "id": "rs_old", "encrypted_content": "opaque", "summary": []any{map[string]any{"type": "summary_text", "text": "proactive summary"}}},
			map[string]any{"type": "function_call", "id": "fc_old", "call_id": "call_1", "name": "tool", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "id": "fco_old", "call_id": "call_1", "output": "tool result"},
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(guardian.URL+"/v1/responses", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Sanitization"); got != "proactive" {
		t.Fatalf("sanitization header = %q", got)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery"); got != "" {
		t.Fatalf("unexpected recovery retry header = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestGuardianAlwaysModeSanitizesZstdBeforeFirstForward(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Header.Get("Content-Encoding"); got != "" {
			t.Fatalf("proactive forward content-encoding = %q", got)
		}
		if !json.Valid(body) || bytes.Contains(body, []byte("encrypted_content")) {
			t.Fatalf("proactive body was not decoded and sanitized: %q", body)
		}
		_, _ = w.Write([]byte(`{"id":"resp_ok"}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeAlways).Handler())
	defer guardian.Close()

	raw := []byte(`{"previous_response_id":"resp_old","input":[{"type":"reasoning","encrypted_content":"opaque"},{"role":"user","content":"continue"}]}`)
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(raw, nil)
	encoder.Close()

	req, _ := http.NewRequest(http.MethodPost, guardian.URL+"/v1/responses", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Sanitization"); got != "proactive" {
		t.Fatalf("sanitization header = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestGuardianAlwaysModeDoesNotRepeatSanitizedRequest(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"The requested item was created under a different *** OpenAI resource."}}`))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	guardian := httptest.NewServer(NewGuardian(upstreamURL, 1<<20, recoveryModeAlways).Handler())
	defer guardian.Close()

	resp, err := http.Post(guardian.URL+"/v1/responses", "application/json", strings.NewReader(`{"previous_response_id":"resp_old","input":[{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Responses-Guardian-Recovery"); got != "not-retried-after-proactive-sanitization" {
		t.Fatalf("recovery header = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestParseRecoveryMode(t *testing.T) {
	if mode, err := parseRecoveryMode("ALWAYS"); err != nil || mode != recoveryModeAlways {
		t.Fatalf("parse always: mode=%q err=%v", mode, err)
	}
	if _, err := parseRecoveryMode("unsafe"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
}
