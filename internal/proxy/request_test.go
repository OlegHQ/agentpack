package proxy

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTranslationMatchesRustGolden(t *testing.T) {
	data, err := os.ReadFile("../../crates/claude-code-proxy-rs/tests/golden/fixtures/request_complex.json")
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	got, err := TranslateAnthropic(request, TranslateOptions{SessionID: "sess_golden", ServiceTier: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONHash(t, got, "e089cd036b3682f572e702b933044d470c75c1d260d7aa0921098321347d2c89")
}

func TestTranslationKeepsEmptyInstructionsAndAcceptsXHigh(t *testing.T) {
	request := map[string]any{"model": "gpt-5.4", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "output_config": map[string]any{"effort": "xhigh"}}
	translated, err := TranslateAnthropic(request, TranslateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if translated["instructions"] != "" || stringValue(object(translated["reasoning"])["effort"]) != "xhigh" {
		t.Fatalf("translation=%#v", translated)
	}
}

func TestTranslationPreservesNoneReasoningEffort(t *testing.T) {
	request := map[string]any{"model": "gpt-5.4", "messages": []any{}, "output_config": map[string]any{"effort": "none"}}
	translated, err := TranslateAnthropic(request, TranslateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(object(translated["reasoning"])["effort"]) != "none" {
		t.Fatalf("translation=%#v", translated)
	}
}
