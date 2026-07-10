package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mockDockerAPI creates a test server that mimics Docker API responses.
func mockDockerAPI(t *testing.T, containers []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			json.NewEncoder(w).Encode(containers)
		case len(r.URL.Path) > len("/containers/") && r.URL.Path[len(r.URL.Path)-5:] == "/json":
			// /containers/{id}/json — return first container
			if len(containers) > 0 {
				json.NewEncoder(w).Encode(containers[0])
			} else {
				w.WriteHeader(404)
			}
		case r.URL.Path == "/events":
			// Hold connection open (simulate event stream)
			flusher, ok := w.(http.Flusher)
			if ok {
				flusher.Flush()
			}
			// Block until client disconnects
			<-r.Context().Done()
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestContainerPorts(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
		want     int // expected number of port bindings
	}{
		{
			name: "single port binding",
			response: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"sandbox": map[string]any{"IPAddress": "172.32.0.10"},
					},
					"Ports": map[string]any{
						"8000/tcp": []map[string]string{{"HostPort": "8000"}},
					},
				},
			},
			want: 1,
		},
		{
			name: "multiple port bindings",
			response: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"sandbox": map[string]any{"IPAddress": "172.32.0.10"},
					},
					"Ports": map[string]any{
						"8000/tcp": []map[string]string{{"HostPort": "8000"}},
						"5173/tcp": []map[string]string{{"HostPort": "5173"}},
					},
				},
			},
			want: 2,
		},
		{
			name: "no port bindings (nil)",
			response: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"sandbox": map[string]any{"IPAddress": "172.32.0.10"},
					},
					"Ports": map[string]any{
						"8080/tcp": nil,
					},
				},
			},
			want: 0,
		},
		{
			name: "no network IP",
			response: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{},
					"Ports": map[string]any{
						"8000/tcp": []map[string]string{{"HostPort": "8000"}},
					},
				},
				"HostConfig": map[string]any{"PortBindings": map[string]any{}},
			},
			want: 0,
		},
		{
			name: "fallback to HostConfig.PortBindings when NetworkSettings.Ports is null",
			response: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"sandbox": map[string]any{"IPAddress": "172.32.0.14"},
					},
					"Ports": map[string]any{
						"8000/tcp": nil,
					},
				},
				"HostConfig": map[string]any{
					"PortBindings": map[string]any{
						"8000/tcp": []map[string]string{{"HostIp": "", "HostPort": "8000"}},
					},
				},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer srv.Close()

			// Override DOCKER_HOST for test
			t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

			ports := containerPorts("test-id")
			if len(ports) != tt.want {
				t.Errorf("got %d ports, want %d", len(ports), tt.want)
			}
		})
	}
}

func TestForwarderLifecycle(t *testing.T) {
	// Start a mock backend
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backendPort := backend.Addr().(*net.TCPAddr).Port

	// Accept connections on backend and echo
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // echo
			}(conn)
		}
	}()

	// Find a free port for the forwarder
	tmp, _ := net.Listen("tcp", "127.0.0.1:0")
	fwdPort := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()

	// Start forwarder
	go startForwarder(fwdPort, "127.0.0.1", backendPort)
	time.Sleep(200 * time.Millisecond)

	// Verify it's in the active map
	mu.Lock()
	_, exists := forwarders[fwdPort]
	mu.Unlock()
	if !exists {
		t.Fatal("forwarder not in active map")
	}

	// Connect and verify echo
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort))
	if err != nil {
		t.Fatal("connect to forwarder:", err)
	}
	msg := []byte("hello port-forward")
	conn.Write(msg)
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := io.ReadFull(conn, buf)
	if err != nil || string(buf[:n]) != string(msg) {
		t.Errorf("echo failed: got %q, err=%v", buf[:n], err)
	}
	conn.Close()

	// Stop forwarder
	stopForwarder(fwdPort)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	_, exists = forwarders[fwdPort]
	mu.Unlock()
	if exists {
		t.Error("forwarder still in active map after stop")
	}

	// Verify port is released
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort))
	if err != nil {
		t.Error("port not released after stop:", err)
	} else {
		ln.Close()
	}
}

func TestForwarderBindConflict(t *testing.T) {
	// Occupy a port
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port

	// Try to start forwarder on occupied port — should not panic or add to active
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		startForwarder(port, "127.0.0.1", 9999)
	}()
	wg.Wait()

	mu.Lock()
	_, exists := forwarders[port]
	mu.Unlock()
	if exists {
		t.Error("forwarder should not be active on occupied port")
	}
}

func TestStopNonexistent(t *testing.T) {
	// Should not panic
	stopForwarder(99999)
}

func TestConcurrentForwarders(t *testing.T) {
	// Start a backend
	backend, _ := net.Listen("tcp", "127.0.0.1:0")
	defer backend.Close()
	backendPort := backend.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(conn)
		}
	}()

	// Start multiple forwarders
	ports := make([]int, 3)
	for i := range ports {
		tmp, _ := net.Listen("tcp", "127.0.0.1:0")
		ports[i] = tmp.Addr().(*net.TCPAddr).Port
		tmp.Close()
		go startForwarder(ports[i], "127.0.0.1", backendPort)
	}
	time.Sleep(300 * time.Millisecond)

	// All should be active
	mu.Lock()
	for _, p := range ports {
		if _, ok := forwarders[p]; !ok {
			t.Errorf("port %d not in active map", p)
		}
	}
	mu.Unlock()

	// Stop all
	for _, p := range ports {
		stopForwarder(p)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if len(forwarders) != 0 {
		t.Errorf("expected 0 active forwarders, got %d", len(forwarders))
	}
	mu.Unlock()
}

func TestScanExistingRetries(t *testing.T) {
	// Simulate: first 2 requests fail, third succeeds with a container
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			attempt++
			if attempt < 3 {
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "test-container-abc"},
			})
			return
		}
		// /containers/test-container-abc/json — return port info
		json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"sandbox": map[string]any{"IPAddress": "172.32.0.99"}},
				"Ports":    map[string]any{"9999/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"9999/tcp": []map[string]string{{"HostIp": "", "HostPort": "19876"}},
				},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	scanExisting()

	// Should have retried and eventually found the container
	if attempt < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempt)
	}

	// Should have set up forwarding for port 19876
	mu.Lock()
	_, exists := forwarders[19876]
	mu.Unlock()
	if !exists {
		t.Error("expected forwarder on port 19876 after retry")
	}

	// Cleanup
	stopForwarder(19876)
}

func TestScanExistingRetriesOnEmptyList(t *testing.T) {
	// Simulate: API responds OK but returns empty list first 3 times, then returns containers
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			attempt++
			if attempt < 4 {
				json.NewEncoder(w).Encode([]map[string]any{}) // empty list
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "delayed-container"},
			})
			return
		}
		// /containers/delayed-container/json
		json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"sandbox": map[string]any{"IPAddress": "172.32.0.50"}},
				"Ports":    map[string]any{"3000/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"3000/tcp": []map[string]string{{"HostIp": "", "HostPort": "19877"}},
				},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	scanExisting()

	if attempt < 4 {
		t.Errorf("expected at least 4 attempts (3 empty + 1 with containers), got %d", attempt)
	}

	mu.Lock()
	_, exists := forwarders[19877]
	mu.Unlock()
	if !exists {
		t.Error("expected forwarder on port 19877 after empty-list retries")
	}

	stopForwarder(19877)
}
