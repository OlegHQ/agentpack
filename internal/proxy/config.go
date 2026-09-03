package proxy

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Transport string

const (
	TransportHTTP      Transport = "http"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

type Config struct {
	Port                                                 int
	ClientToken                                          string
	Transport                                            Transport
	RequestTimeout, ConnectTimeout, WebSocketIdleTimeout time.Duration
	Models                                               ModelMap
	Diagnostics                                          DiagnosticsConfig
}

func ConfigFromEnvironment(token string) Config {
	transport := Transport(strings.ToLower(strings.TrimSpace(os.Getenv("AGENTPACK_PROXY_TRANSPORT"))))
	if transport != TransportHTTP && transport != TransportAuto {
		transport = TransportWebSocket
	}
	return Config{Port: envInt("AGENTPACK_PROXY_PORT", 0), ClientToken: token, Transport: transport, RequestTimeout: time.Duration(envInt("AGENTPACK_PROXY_REQUEST_TIMEOUT_SECS", 300)) * time.Second, ConnectTimeout: time.Duration(envInt("AGENTPACK_PROXY_WS_CONNECT_TIMEOUT_SECS", 15)) * time.Second, WebSocketIdleTimeout: time.Duration(envInt("AGENTPACK_PROXY_WS_IDLE_TIMEOUT_SECS", 300)) * time.Second, Models: ModelMapFromEnvironment(), Diagnostics: DiagnosticsConfig{LogPayloads: envBool("AGENTPACK_PROXY_LOG_PAYLOADS"), MaxBodyBytes: envInt("AGENTPACK_PROXY_LOG_MAX_BODY_BYTES", 4096)}}
}
func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}
func envBool(key string) bool {
	return oneOf(strings.ToLower(strings.TrimSpace(os.Getenv(key))), "1", "true", "yes")
}
