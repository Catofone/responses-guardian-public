package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type Guardian struct {
	upstream  *url.URL
	client    *http.Client
	reverse   *httputil.ReverseProxy
	maxBody   int64
	mode      atomic.Value // recoveryMode; each request snapshots it once.
	modeFile  string       // Set before the HTTP server starts.
	dashboard *Dashboard   // Optional, initialized before the HTTP server starts.

	forwarded             atomic.Uint64
	passthroughRequests   atomic.Uint64
	modeChanges           atomic.Uint64
	proactiveChecks       atomic.Uint64
	proactiveSanitization atomic.Uint64
	recoveryAttempts      atomic.Uint64
	recoverySuccess       atomic.Uint64
	recoveryFailed        atomic.Uint64
}

type recoveryMode string

const (
	recoveryModeOff     recoveryMode = "off"
	recoveryModeOnError recoveryMode = "on-error"
	recoveryModeAlways  recoveryMode = "always"
)

func parseRecoveryMode(value string) (recoveryMode, error) {
	mode := recoveryMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case recoveryModeOff, recoveryModeOnError, recoveryModeAlways:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid mode: expected off, on-error or always")
	}
}

func NewGuardian(upstream *url.URL, maxBody int64, mode recoveryMode) *Guardian {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 10 * time.Minute,
		DisableCompression:    true,
	}
	reverse := httputil.NewSingleHostReverseProxy(upstream)
	director := reverse.Director
	reverse.Director = func(r *http.Request) {
		director(r)
		r.Host = upstream.Host
	}
	reverse.Transport = transport
	reverse.FlushInterval = -1
	reverse.ErrorLog = log.New(io.Discard, "", 0)
	reverse.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("upstream transport error: %v", err)
		http.Error(w, "Responses Guardian could not reach CCSwitch", http.StatusBadGateway)
	}

	guardian := &Guardian{
		upstream: upstream,
		client:   &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		reverse:  reverse,
		maxBody:  maxBody,
	}
	guardian.mode.Store(mode)
	return guardian
}

func (g *Guardian) currentMode() recoveryMode {
	return g.mode.Load().(recoveryMode)
}

func (g *Guardian) setMode(mode recoveryMode) {
	previous := g.mode.Swap(mode).(recoveryMode)
	if previous != mode {
		g.modeChanges.Add(1)
		log.Printf("mode changed: %s -> %s; applies to new requests", previous, mode)
	}
}

func (g *Guardian) Handler() http.Handler {
	return http.HandlerFunc(g.serveHTTP)
}

func (g *Guardian) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__responses_guardian/") && r.URL.Path != "/__responses_guardian/health" && g.dashboard != nil {
		g.dashboard.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/__responses_guardian/health" {
		g.writeHealth(w)
		return
	}

	g.forwarded.Add(1)
	mode := g.currentMode()
	if mode == recoveryModeOff {
		g.passthroughRequests.Add(1)
		g.reverse.ServeHTTP(w, r)
		return
	}
	if !isResponsesRequest(r) {
		g.reverse.ServeHTTP(w, r)
		return
	}

	body, err := readRequestBody(w, r, g.maxBody)
	if err != nil {
		return
	}

	forwardBody := body
	forwardDecoded := false
	proactiveReport := SanitizeReport{}
	proactiveChecked := mode == recoveryModeAlways
	proactiveChanged := false
	if proactiveChecked {
		g.proactiveChecks.Add(1)
		decodedBody, wasEncoded, decodeErr := decodeRequestBody(body, requestContentEncoding(r.Header), g.maxBody)
		if decodeErr != nil {
			log.Printf("proactive history sanitization rejected request: decode body: %v", decodeErr)
			http.Error(w, "Responses Guardian could not decode request for proactive sanitization", http.StatusUnsupportedMediaType)
			return
		}
		sanitized, report, sanitizeErr := SanitizeResponsesRequest(decodedBody)
		if sanitizeErr != nil {
			log.Printf("proactive history sanitization rejected request: %v", sanitizeErr)
			http.Error(w, "Responses Guardian could not parse request for proactive sanitization", http.StatusBadRequest)
			return
		}
		proactiveReport = report
		if report.Changed() {
			forwardBody = sanitized
			forwardDecoded = wasEncoded
			proactiveChanged = true
			g.proactiveSanitization.Add(1)
			log.Printf(
				"proactive history sanitization: removed_items=%d preserved_summaries=%d cleared_references=%d stripped_metadata=%d",
				report.RemovedItems,
				report.PreservedSummaries,
				report.ClearedReferences,
				report.StrippedMetadata,
			)
		}
	}

	first, err := g.forwardBuffered(r, forwardBody, forwardDecoded)
	if err != nil {
		log.Printf("CCSwitch transport error: %v", err)
		http.Error(w, "Responses Guardian could not reach CCSwitch", http.StatusBadGateway)
		return
	}

	if first.StatusCode != http.StatusBadRequest {
		copyUpstreamResponse(w, first, proactiveResponseHeaders(proactiveChecked, proactiveChanged, proactiveReport))
		return
	}

	errorBody, err := io.ReadAll(io.LimitReader(first.Body, g.maxBody+1))
	_ = first.Body.Close()
	if err != nil {
		http.Error(w, "Responses Guardian could not read the CCSwitch error", http.StatusBadGateway)
		return
	}
	errorKind, recoverable := classifyRecoverableHistoryError(first.StatusCode, errorBody)
	if int64(len(errorBody)) > g.maxBody || !recoverable {
		copyBufferedResponse(w, first, errorBody, proactiveResponseHeaders(proactiveChecked, proactiveChanged, proactiveReport))
		return
	}
	if proactiveChecked {
		headers := proactiveResponseHeaders(true, proactiveChanged, proactiveReport)
		headers.Set("X-Responses-Guardian-Recovery", "not-retried-after-proactive-sanitization")
		headers.Set("X-Responses-Guardian-Recovery-Cause", string(errorKind))
		log.Printf("history recovery (%s) not retried because proactive sanitization already ran", errorKind)
		copyBufferedResponse(w, first, errorBody, headers)
		return
	}

	decodedBody, wasEncoded, decodeErr := decodeRequestBody(body, requestContentEncoding(r.Header), g.maxBody)
	if decodeErr != nil {
		log.Printf("history recovery (%s) skipped: decode request body: %v", errorKind, decodeErr)
		copyBufferedResponse(w, first, errorBody, nil)
		return
	}
	sanitized, report, err := SanitizeResponsesRequest(decodedBody)
	if err != nil || !report.Changed() {
		if err != nil {
			log.Printf("history recovery (%s) skipped: %v", errorKind, err)
		}
		copyBufferedResponse(w, first, errorBody, nil)
		return
	}

	g.recoveryAttempts.Add(1)
	log.Printf(
		"history recovery (%s): removed_items=%d preserved_summaries=%d cleared_references=%d stripped_metadata=%d; retrying once",
		errorKind,
		report.RemovedItems,
		report.PreservedSummaries,
		report.ClearedReferences,
		report.StrippedMetadata,
	)

	retry, retryErr := g.forwardBuffered(r, sanitized, wasEncoded)
	if retryErr != nil {
		g.recoveryFailed.Add(1)
		log.Printf("history recovery (%s) transport failure: %v", errorKind, retryErr)
		copyBufferedResponse(w, first, errorBody, http.Header{
			"X-Responses-Guardian-Recovery":       []string{"transport-failed"},
			"X-Responses-Guardian-Recovery-Cause": []string{string(errorKind)},
		})
		return
	}

	headers := http.Header{
		"X-Responses-Guardian-Recovery":            []string{"attempted"},
		"X-Responses-Guardian-Recovery-Cause":      []string{string(errorKind)},
		"X-Responses-Guardian-Removed-Items":       []string{fmt.Sprintf("%d", report.RemovedItems)},
		"X-Responses-Guardian-Preserved-Summaries": []string{fmt.Sprintf("%d", report.PreservedSummaries)},
	}
	if retry.StatusCode >= 200 && retry.StatusCode < 300 {
		g.recoverySuccess.Add(1)
		headers.Set("X-Responses-Guardian-Recovery", "succeeded")
		log.Printf("history recovery (%s) succeeded", errorKind)
	} else {
		g.recoveryFailed.Add(1)
		log.Printf("history recovery (%s) returned HTTP %d; no further retry", errorKind, retry.StatusCode)
	}
	copyUpstreamResponse(w, retry, headers)
}

func (g *Guardian) forwardBuffered(original *http.Request, body []byte, decodedForRecovery bool) (*http.Response, error) {
	target := *g.upstream
	target.Path = joinURLPath(g.upstream.Path, original.URL.Path)
	target.RawPath = ""
	target.RawQuery = original.URL.RawQuery

	req, err := http.NewRequestWithContext(original.Context(), original.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, original.Header)
	removeHopByHopHeaders(req.Header)
	if decodedForRecovery {
		req.Header.Del("Content-Encoding")
	}
	req.Header.Del("Content-Length")
	req.Host = g.upstream.Host
	req.ContentLength = int64(len(body))
	return g.client.Do(req)
}

func (g *Guardian) writeHealth(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":                "ok",
		"version":               version,
		"mode":                  g.currentMode(),
		"pid":                   os.Getpid(),
		"supported_modes":       []recoveryMode{recoveryModeOff, recoveryModeOnError, recoveryModeAlways},
		"mode_switch_supported": g.modeFile != "",
		"mode_file":             g.modeFile,
		"mode_changes":          g.modeChanges.Load(),
		"upstream":              g.upstream.String(),
		"forwarded":             g.forwarded.Load(),
		"passthrough_requests":  g.passthroughRequests.Load(),
		"proactive_checks":      g.proactiveChecks.Load(),
		"proactive_changes":     g.proactiveSanitization.Load(),
		"recovery_attempts":     g.recoveryAttempts.Load(),
		"recovery_success":      g.recoverySuccess.Load(),
		"recovery_failed":       g.recoveryFailed.Load(),
	})
}

func proactiveResponseHeaders(checked, changed bool, report SanitizeReport) http.Header {
	if !checked {
		return nil
	}
	result := "unchanged"
	if changed {
		result = "proactive"
	}
	return http.Header{
		"X-Responses-Guardian-Sanitization":        []string{result},
		"X-Responses-Guardian-Removed-Items":       []string{fmt.Sprintf("%d", report.RemovedItems)},
		"X-Responses-Guardian-Preserved-Summaries": []string{fmt.Sprintf("%d", report.PreservedSummaries)},
	}
}

func readRequestBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		var status = http.StatusBadRequest
		if _, ok := err.(*http.MaxBytesError); ok {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "Responses Guardian could not read request body", status)
		return nil, err
	}
	return body, nil
}

func isResponsesRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimSuffix(strings.ToLower(r.URL.Path), "/")
	return strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
}

type recoverableHistoryErrorKind string

const (
	historyErrorEncryptedContent recoverableHistoryErrorKind = "encrypted-content"
	historyErrorResourceMismatch recoverableHistoryErrorKind = "resource-mismatch"
)

func classifyRecoverableHistoryError(status int, body []byte) (recoverableHistoryErrorKind, bool) {
	if status != http.StatusBadRequest {
		return "", false
	}
	message := strings.ToLower(string(body))
	if strings.Contains(message, "invalid_encrypted_content") {
		return historyErrorEncryptedContent, true
	}
	if strings.Contains(message, "encrypted content") || strings.Contains(message, "encrypted_content") {
		if strings.Contains(message, "could not be decrypted") ||
			strings.Contains(message, "could not be verified") ||
			strings.Contains(message, "could not be parsed") ||
			strings.Contains(message, "cannot decrypt") ||
			strings.Contains(message, "failed to decrypt") {
			return historyErrorEncryptedContent, true
		}
	}

	createdUnderDifferentResource :=
		strings.Contains(message, "requested item was created under a different") &&
			strings.Contains(message, "openai resource")
	useSameCreatingResource :=
		strings.Contains(message, "use the same resource that created the item")
	if createdUnderDifferentResource || useSameCreatingResource {
		return historyErrorResourceMismatch, true
	}

	return "", false
}

func copyUpstreamResponse(w http.ResponseWriter, resp *http.Response, extra http.Header) {
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	removeHopByHopHeaders(w.Header())
	copyHeaders(w.Header(), extra)
	w.WriteHeader(resp.StatusCode)
	flushCopy(w, resp.Body)
}

func copyBufferedResponse(w http.ResponseWriter, resp *http.Response, body []byte, extra http.Header) {
	copyHeaders(w.Header(), resp.Header)
	removeHopByHopHeaders(w.Header())
	copyHeaders(w.Header(), extra)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func flushCopy(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(header http.Header) {
	if connection := header.Get("Connection"); connection != "" {
		for _, token := range strings.Split(connection, ",") {
			header.Del(strings.TrimSpace(token))
		}
	}
	for _, key := range hopByHopHeaders {
		header.Del(key)
	}
}

func joinURLPath(basePath, requestPath string) string {
	baseSlash := strings.HasSuffix(basePath, "/")
	requestSlash := strings.HasPrefix(requestPath, "/")
	switch {
	case baseSlash && requestSlash:
		return basePath + requestPath[1:]
	case !baseSlash && !requestSlash:
		return basePath + "/" + requestPath
	default:
		return basePath + requestPath
	}
}
