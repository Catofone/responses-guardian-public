package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeResponsesRequestPreservesVisibleAndToolHistory(t *testing.T) {
	raw := []byte(`{
  "model":"gpt-test",
  "previous_response_id":"resp_old",
  "conversation":"conv_old",
  "include":["reasoning.encrypted_content"],
  "input":[
    {"type":"message","id":"msg_1","status":"completed","role":"user","content":[{"type":"input_text","text":"fix it"}]},
    {"type":"reasoning","id":"rs_1","encrypted_content":"gAAAA","summary":[{"type":"summary_text","text":"Inspect the parser first."}]},
    {"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"read_file","arguments":"{}"},
    {"type":"function_call_output","id":"fco_1","call_id":"call_1","output":"source"},
    {"type":"compaction","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"Earlier context summary."}]},
    {"type":"item_reference","id":"item_old"},
    {"type":"message","id":"msg_2","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}
  ]
}`)

	cleaned, report, err := SanitizeResponsesRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedItems != 3 {
		t.Fatalf("removed items = %d, want 3", report.RemovedItems)
	}
	if report.PreservedSummaries != 2 {
		t.Fatalf("preserved summaries = %d, want 2", report.PreservedSummaries)
	}
	if report.ClearedReferences != 2 {
		t.Fatalf("cleared references = %d, want 2", report.ClearedReferences)
	}

	var body map[string]any
	if err := json.Unmarshal(cleaned, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["previous_response_id"]; exists {
		t.Fatal("previous_response_id was not removed")
	}
	if _, exists := body["conversation"]; exists {
		t.Fatal("conversation was not removed")
	}
	if got := body["include"].([]any)[0]; got != "reasoning.encrypted_content" {
		t.Fatalf("include changed: %v", got)
	}

	encoded := string(cleaned)
	for _, forbidden := range []string{"gAAAA", "opaque", `"type":"reasoning"`, `"type":"compaction"`, `"type":"item_reference"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("sanitized request still contains %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{"call_1", "read_file", "source", "Inspect the parser first.", "Earlier context summary.", "done"} {
		if !strings.Contains(encoded, required) {
			t.Fatalf("sanitized request lost %q: %s", required, encoded)
		}
	}
	if strings.Contains(encoded, `"id":"`) || strings.Contains(encoded, `"status":"`) {
		t.Fatalf("server metadata was not stripped: %s", encoded)
	}
}

func TestSanitizeResponsesRequestNoChange(t *testing.T) {
	raw := []byte(`{"model":"gpt-test","input":"hello"}`)
	cleaned, report, err := SanitizeResponsesRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed() {
		t.Fatalf("unexpected report: %+v", report)
	}
	if string(cleaned) != string(raw) {
		t.Fatalf("unchanged request was rewritten")
	}
}

func TestEncryptedContentErrorDetection(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
		kind   recoverableHistoryErrorKind
	}{
		{"code", 400, `{"error":{"code":"invalid_encrypted_content"}}`, true, historyErrorEncryptedContent},
		{"message", 400, `The encrypted content abc could not be verified. Reason: Encrypted content could not be decrypted or parsed.`, true, historyErrorEncryptedContent},
		{"resource mismatch", 400, `{"error":{"message":"The requested item was created under a different abc OpenAI resource. Use the same resource that created the item to access it."}}`, true, historyErrorResourceMismatch},
		{"wrapped resource mismatch", 400, `{"error":{"code":"cc_switch_upstream_error","cause":"OpenAI Responses bad request: The requested item was created under a different *** OpenAI resource."}}`, true, historyErrorResourceMismatch},
		{"generic different resource", 400, `{"error":{"message":"file was created under a different resource"}}`, false, ""},
		{"generic bad request", 400, `{"error":{"message":"invalid model"}}`, false, ""},
		{"server error", 500, `invalid_encrypted_content`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, got := classifyRecoverableHistoryError(tc.status, []byte(tc.body))
			if got != tc.want || kind != tc.kind {
				t.Fatalf("got (%q, %v), want (%q, %v)", kind, got, tc.kind, tc.want)
			}
		})
	}
}
