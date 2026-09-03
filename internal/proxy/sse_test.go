package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestCodexStreamFunctionsMatchRustOracle(t *testing.T) {
	tests := []struct{ fixture, reduce, accumulate, sse string }{
		{"codex_text.sse", "761a36b3c5ba0e49a4ae1af992b908c68aed442e997bf46a87fc48d424f28ba8", "c37a09dc2d3d8969d217d8472f78fc5d95c3d9e70d1980c146b34677585a662b", "660909dfc895b6ff00aeb92e52b43103704cc23fd42254c176a64e0c3429f3b4"},
		{"codex_tool.sse", "033a1ebbf1f0aab1bfcd3812dc4248f393d639085758a71d44af91bce38eef14", "3ae2c8b3235a757c37eaa3bef0b68e0dcb82b5f6c84bc4f0e1b3fa9125ab6e55", "0aaeb8f0f6290b3e2537480904dd38cf4852e7270198afec062f427644e436cd"},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			input, err := os.ReadFile("../../crates/claude-code-proxy-rs/tests/golden/fixtures/" + test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			reduced, err := ReduceCodexSSE(input)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONHash(t, reduced, test.reduce)
			accumulated, err := AccumulateCodex(input, "msg_golden", "gpt-5.4")
			if err != nil {
				t.Fatal(err)
			}
			assertJSONHash(t, accumulated, test.accumulate)
			chunks, err := CodexToAnthropicSSE(input, "msg_golden", "gpt-5.4")
			if err != nil {
				t.Fatal(err)
			}
			var joined []byte
			for _, chunk := range chunks {
				joined = append(joined, chunk...)
			}
			assertHash(t, joined, test.sse)
		})
	}
}

func assertJSONHash(t *testing.T, value any, expected string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	assertHash(t, data, expected)
}
func assertHash(t *testing.T, data []byte, expected string) {
	t.Helper()
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != expected {
		t.Fatalf("hash=%s expected=%s\n%s", got, expected, data)
	}
}
