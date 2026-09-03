package proxy

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTranslationMatchesGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/request_complex.json")
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
	assertJSONHash(t, got, "b1665c3dbc4d85daea98507d381ee053cf9720a1674ef0700557d5395341449a")
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
