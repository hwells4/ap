package runner

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CallbackListenerConfig configures the ephemeral callback listener.
type CallbackListenerConfig struct {
	// Host is the address to bind to (e.g., "127.0.0.1" or "10.0.0.5").
	Host string

	// Timeout is the maximum time to wait for a callback response.
	Timeout time.Duration
}

// CallbackResponse is the JSON body received on POST /resume.
type CallbackResponse struct {
	Option string `json:"option"`
}

// CallbackListener is an ephemeral HTTP server that waits for a single
// POST /resume callback from an escalation webhook consumer.
type CallbackListener struct {
	server   *http.Server
	listener net.Listener
	url      string
	token    string
	timeout  time.Duration

	resultCh chan CallbackResponse
	once     sync.Once
	done     chan struct{}
}

// NewCallbackListener creates and starts a callback listener on an ephemeral port.
// For non-localhost hosts, a bearer token is generated for authentication.
func NewCallbackListener(cfg CallbackListenerConfig) (*CallbackListener, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}

	// Always bind to 127.0.0.1 locally; the host is used in the advertised URL.
	// When host is non-localhost (e.g., a Tailscale IP), the caller is
	// responsible for routing traffic to this machine.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("callback listener bind: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	addr := ln.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://%s:%d/resume", host, addr.Port)

	var token string
	if !isLocalhost(host) {
		token, err = generateToken()
		if err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("generate callback token: %w", err)
		}
	}

	cl := &CallbackListener{
		listener: ln,
		url:      url,
		token:    token,
		timeout:  timeout,
		resultCh: make(chan CallbackResponse, 1),
		done:     make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/resume", cl.handleResume)

	cl.server = &http.Server{Handler: mux}
	go func() { _ = cl.server.Serve(ln) }()

	return cl, nil
}

// URL returns the full callback URL (e.g., "http://127.0.0.1:41823/resume").
func (cl *CallbackListener) URL() string {
	return cl.url
}

// Token returns the bearer token for authentication, or empty if localhost.
func (cl *CallbackListener) Token() string {
	return cl.token
}

// LocalURL returns the actual bound address URL, used for testing when the
// advertised host differs from the bind address.
func (cl *CallbackListener) LocalURL() string {
	addr := cl.listener.Addr().(*net.TCPAddr)
	return fmt.Sprintf("http://127.0.0.1:%d/resume", addr.Port)
}

// Wait blocks until a valid callback is received or the timeout expires.
// Returns the callback response or an error on timeout/close.
func (cl *CallbackListener) Wait() (CallbackResponse, error) {
	timer := time.NewTimer(cl.timeout)
	defer timer.Stop()

	select {
	case result := <-cl.resultCh:
		return result, nil
	case <-timer.C:
		return CallbackResponse{}, fmt.Errorf("callback listener timeout after %v", cl.timeout)
	case <-cl.done:
		return CallbackResponse{}, fmt.Errorf("callback listener closed")
	}
}

// Close shuts down the listener.
func (cl *CallbackListener) Close() {
	cl.once.Do(func() {
		close(cl.done)
		_ = cl.server.Close()
	})
}

func (cl *CallbackListener) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if cl.token != "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(cl.token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var resp CallbackResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &resp); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	select {
	case cl.resultCh <- resp:
		w.WriteHeader(http.StatusOK)
	default:
		// Already received a response; ignore duplicates.
		w.WriteHeader(http.StatusConflict)
	}
}

func isLocalhost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
