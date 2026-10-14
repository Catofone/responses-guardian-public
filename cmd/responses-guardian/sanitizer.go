package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type SanitizeReport struct {
	RemovedItems       int
	PreservedSummaries int
	ClearedReferences  int
	StrippedMetadata   int
}

func (r SanitizeReport) Changed() bool {
	return r.RemovedItems > 0 || r.ClearedReferences > 0 || r.StrippedMetadata > 0
}

func SanitizeResponsesRequest(raw []byte) ([]byte, SanitizeReport, error) {
	var body map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, SanitizeReport{}, fmt.Errorf("parse Responses request: %w", err)
	}

	report := SanitizeReport{}
	for _, key := range []string{"previous_response_id", "conversation"} {
		if value, ok := body[key]; ok && value != nil {
			delete(body, key)
			report.ClearedReferences++
		}
	}

	if input, ok := body["input"].([]any); ok {
		cleaned := make([]any, 0, len(input))
		for _, item := range input {
			object, isObject := item.(map[string]any)
			if !isObject {
				cleaned = append(cleaned, item)
				continue
			}

			itemType := strings.ToLower(stringValue(object["type"]))
			if shouldRemoveOpaqueItem(itemType, object) {
				if summary := extractPlainSummary(object); summary != "" {
					cleaned = append(cleaned, recoveredSummaryMessage(summary))
					report.PreservedSummaries++
				}
				report.RemovedItems++
				continue
			}

			for _, key := range []string{"id", "status"} {
				if _, exists := object[key]; exists {
					delete(object, key)
					report.StrippedMetadata++
				}
			}
			cleaned = append(cleaned, object)
		}
		body["input"] = cleaned
	}

	if !report.Changed() {
		return raw, report, nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, SanitizeReport{}, fmt.Errorf("encode sanitized Responses request: %w", err)
	}
	return encoded, report, nil
}

func shouldRemoveOpaqueItem(itemType string, item map[string]any) bool {
	if itemType == "reasoning" || itemType == "item_reference" || strings.Contains(itemType, "compaction") {
		return true
	}
	if value, exists := item["encrypted_content"]; exists && stringValue(value) != "" {
		return true
	}
	return false
}

func extractPlainSummary(item map[string]any) string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 4)
	appendText := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if _, exists := seen[text]; exists {
			return
		}
		seen[text] = struct{}{}
		parts = append(parts, text)
	}

	if value, ok := item["summary"].(string); ok {
		appendText(value)
	}
	if value, ok := item["reasoning_content"].(string); ok {
		appendText(value)
	}
	for _, field := range []string{"summary", "content"} {
		if values, ok := item[field].([]any); ok {
			for _, value := range values {
				if object, ok := value.(map[string]any); ok {
					appendText(stringValue(object["text"]))
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func recoveredSummaryMessage(summary string) map[string]any {
	return map[string]any{
		"role":    "assistant",
		"content": "[Recovered reasoning summary from the previous upstream]\n" + summary,
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
