package runner

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCallbackListener_BindsToEphemeralPort(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	url := listener.URL()
	if url == "" {
		t.Fatal("URL should not be empty")
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want http://127.0.0.1:*", url)
	}
	if !strings.HasSuffix(url, "/resume") {
		t.Fatalf("URL = %q, want */resume suffix", url)
	}
}

func TestCallbackListener_LocalhostNoToken(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	if listener.Token() != "" {
		t.Fatalf("Token should be empty for localhost, got %q", listener.Token())
	}
}

func TestCallbackListener_NonLocalhostGeneratesToken(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "10.0.0.5",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	if listener.Token() == "" {
		t.Fatal("Token should be non-empty for non-localhost host")
	}
	if len(listener.Token()) < 16 {
		t.Fatalf("Token too short: %q", listener.Token())
	}

	// URL should advertise the configured host, not 127.0.0.1.
	if !strings.Contains(listener.URL(), "10.0.0.5") {
		t.Fatalf("URL = %q, want to contain advertised host 10.0.0.5", listener.URL())
	}
}

func TestCallbackListener_AcceptsValidResumePost(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	go func() {
		body := `{"option":"approve"}`
		resp, err := http.Post(listener.URL(), "application/json", strings.NewReader(body))
		if err != nil {
			t.Errorf("POST error: %v", err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	}()

	result, err := listener.Wait()
	if err != nil {
		t.Fatalf("Wait() error: %v", err)
	}
	if result.Option != "approve" {
		t.Fatalf("Option = %q, want approve", result.Option)
	}
}

func TestCallbackListener_RejectsGetMethod(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	resp, err := http.Get(listener.URL())
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestCallbackListener_RequiresTokenForNonLocalhost(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "10.0.0.5",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	token := listener.Token()
	localURL := listener.LocalURL() // use local bind address for actual requests

	// Request without token → 401.
	body := `{"option":"approve"}`
	resp, err := http.Post(localURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token)", resp.StatusCode)
	}

	// Request with wrong token → 401.
	req, _ := http.NewRequest(http.MethodPost, localURL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST error: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (wrong token)", resp2.StatusCode)
	}

	// Request with correct token → 200, listener returns result.
	go func() {
		req, _ := http.NewRequest(http.MethodPost, localURL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("POST error: %v", err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	}()

	result, err := listener.Wait()
	if err != nil {
		t.Fatalf("Wait() error: %v", err)
	}
	if result.Option != "approve" {
		t.Fatalf("Option = %q, want approve", result.Option)
	}
}

func TestCallbackListener_TimeoutReturnsError(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}
	defer listener.Close()

	started := time.Now()
	_, err = listener.Wait()
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("Wait() should return error on timeout")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestCallbackListener_CloseStopsWait(t *testing.T) {
	listener, err := NewCallbackListener(CallbackListenerConfig{
		Host:    "127.0.0.1",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCallbackListener() error: %v", err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		listener.Close()
	}()

	_, err = listener.Wait()
	if err == nil {
		t.Fatal("Wait() should return error after Close()")
	}
}

