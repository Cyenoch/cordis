package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func testControl(t *testing.T) (*controlServer, string) {
	t.Helper()
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connection.json")
	s, err := startControl(t.Context(), h, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s, path
}

func TestControlPrivateConnectionAndAuthentication(t *testing.T) {
	s, path := testControl(t)
	connection, err := readControlConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := startControl(t.Context(), s.host, path); err == nil {
		t.Fatal("second session overwrote an existing connection")
	}
	if after, err := readControlConnection(path); err != nil || after != connection {
		t.Fatal("failed creation changed credentials")
	}
	response, err := requestControl(t.Context(), connection, "status", "", time.Second)
	if err != nil || response.Error != "" || response.Status == nil || !response.Status.Online {
		t.Fatalf("status: %+v %v", response, err)
	}
	encoded, _ := json.Marshal(response)
	if bytes.Contains(encoded, []byte(connection.Token)) {
		t.Fatal("status leaked authentication token")
	}
	connection.Token = strings.Repeat("x", len(connection.Token))
	response, err = requestControl(t.Context(), connection, "status", "", time.Second)
	if err != nil || response.Phase != "rejected" || response.Code != contract.Denied || response.Status != nil {
		t.Fatalf("bad token returned host data: %+v %v", response, err)
	}
}

func TestControlRejectsMalformedAndBrowserRequests(t *testing.T) {
	s, _ := testControl(t)
	body, _ := json.Marshal(controlRequest{Version: 1, Session: s.connection.Session, Action: "status", TimeoutMillis: 1000})
	for _, test := range []struct{ name, body, origin, host string }{
		{"unknown field", string(body[:len(body)-1]) + `,"ignored":true}`, "", s.connection.Address},
		{"oversize", strings.Repeat(" ", contract.MaxFrameBytes+1), "", s.connection.Address},
		{"wrong version", strings.Replace(string(body), `"version":1`, `"version":2`, 1), "", s.connection.Address},
		{"browser", string(body), "https://example.test", s.connection.Address},
		{"dns rebinding", string(body), "", "example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+s.connection.Address+controlPath, strings.NewReader(test.body))
			request.Host = test.host
			request.Header.Set("Authorization", "Bearer "+s.connection.Token)
			request.Header.Set("Origin", test.origin)
			writer := httptest.NewRecorder()
			s.ServeHTTP(writer, request)
			var response controlResponse
			if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Phase != "rejected" || response.Status != nil {
				t.Fatalf("unsafe request accepted: %+v", response)
			}
		})
	}
}

func TestControlNeverUsesProxyOrRedirects(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	t.Setenv("HTTP_PROXY", other.URL)
	t.Setenv("http_proxy", other.URL)
	_, err := requestControl(t.Context(), controlConnection{Address: strings.TrimPrefix(origin.URL, "http://"), Session: "session", Token: "credential"}, "stop", "entry", time.Second)
	if err == nil || !strings.Contains(err.Error(), "unknown") || leaked.Load() != 0 {
		t.Fatalf("redirect or proxy received control credentials: count=%d err=%v", leaked.Load(), err)
	}
}

func TestControlConnectionRejectsRemoteAddressesAndPreservesReplacement(t *testing.T) {
	s, path := testControl(t)
	for _, address := range []string{"example.test:80", "[::1]:80", "127.0.0.2:80", "127.0.0.1:0", "127.0.0.1:080", "127.0.0.1:65536"} {
		connection := s.connection
		connection.Address = address
		data, _ := json.Marshal(connection)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readControlConnection(path); err == nil {
			t.Fatalf("unsafe address accepted: %s", address)
		}
	}
	replacement := []byte(`{"user":"replacement"}`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, replacement) {
		t.Fatalf("close removed a replacement connection: %q %v", data, err)
	}
}

func TestControlCloseCancelsIncompleteRequestAndRemovesOwnFile(t *testing.T) {
	s, path := testControl(t)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+s.connection.Address+controlPath, reader)
		req.Header.Set("Authorization", "Bearer "+s.connection.Token)
		response, err := http.DefaultClient.Do(req)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	// A partial body keeps the handler waiting without submitting any operation.
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not release a blocked body read")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("own connection file survived shutdown: %v", err)
	}
}
