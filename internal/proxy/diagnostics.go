package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DiagnosticsConfig struct {
	LogDirectory string
	LogPayloads  bool
	MaxBodyBytes int
}
type Diagnostics struct {
	mu       sync.Mutex
	file     *os.File
	started  time.Time
	payloads bool
	max      int
}

func NewDiagnostics(config DiagnosticsConfig) (*Diagnostics, error) {
	diagnostics := &Diagnostics{started: time.Now(), payloads: config.LogPayloads, max: config.MaxBodyBytes}
	if diagnostics.max < 256 {
		diagnostics.max = 256
	}
	if config.LogDirectory == "" {
		return diagnostics, nil
	}
	if err := os.MkdirAll(config.LogDirectory, 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("proxy-%s-%d.jsonl", time.Now().UTC().Format("20060102T150405Z"), os.Getpid())
	path := filepath.Join(config.LogDirectory, name)
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	diagnostics.file = file
	latest := map[string]any{"path": path, "started_at": time.Now().UTC().Format(time.RFC3339Nano), "pid": os.Getpid()}
	data, _ := json.MarshalIndent(latest, "", "  ")
	_ = os.WriteFile(filepath.Join(config.LogDirectory, "latest.json"), data, 0o644)
	return diagnostics, nil
}
func (diagnostics *Diagnostics) Event(kind string, fields map[string]any) {
	if diagnostics == nil || diagnostics.file == nil {
		return
	}
	event := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "elapsed_ms": time.Since(diagnostics.started).Milliseconds(), "kind": kind}
	for key, value := range fields {
		event[key] = value
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	_, _ = diagnostics.file.Write(append(data, '\n'))
	_ = diagnostics.file.Sync()
}
func (diagnostics *Diagnostics) Close() {
	if diagnostics != nil && diagnostics.file != nil {
		_ = diagnostics.file.Close()
	}
}
func Snippet(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end] + "..."
}
