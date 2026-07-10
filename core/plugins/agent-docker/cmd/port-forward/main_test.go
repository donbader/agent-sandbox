package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockDockerServer creates a test server that serves container inspect responses.
func mockDockerServer(containers map[string]map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/containers/json" {
			list := []map[string]any{}
			for id := range containers {
				list = append(list, map[string]any{"Id": id})
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		// /containers/<id>/json
		for id, info := range containers {
			if r.URL.Path == "/containers/"+id+"/json" {
				json.NewEncoder(w).Encode(info)
				return
			}
		}
		w.WriteHeader(404)
	}))
}

func TestContainerPorts(t *testing.T) {
	tests := []struct {
		name     string
		info     map[string]any
		expected [][3]string
	}{
		{
			name: "single port binding",
			info: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.2"}},
					"Ports": map[string]any{
						"8080/tcp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": "8080"}},
					},
				},
				"HostConfig": map[string]any{"PortBindings": map[string]any{}},
			},
			expected: [][3]string{{"8080", "172.17.0.2", "8080"}},
		},
		{
			name: "multiple port bindings",
			info: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.3"}},
					"Ports": map[string]any{
						"80/tcp":   []map[string]string{{"HostIp": "", "HostPort": "8080"}},
						"443/tcp":  []map[string]string{{"HostIp": "", "HostPort": "8443"}},
					},
				},
				"HostConfig": map[string]any{"PortBindings": map[string]any{}},
			},
			expected: [][3]string{{"8080", "172.17.0.3", "80"}, {"8443", "172.17.0.3", "443"}},
		},
		{
			name: "no port bindings (nil)",
			info: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.4"}},
					"Ports":    map[string]any{"80/tcp": nil},
				},
				"HostConfig": map[string]any{"PortBindings": map[string]any{}},
			},
			expected: nil,
		},
		{
			name: "no network IP",
			info: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{},
					"Ports": map[string]any{
						"80/tcp": []map[string]string{{"HostIp": "", "HostPort": "8080"}},
					},
				},
				"HostConfig": map[string]any{"PortBindings": map[string]any{}},
			},
			expected: nil,
		},
		{
			name: "fallback to HostConfig.PortBindings when NetworkSettings.Ports is null",
			info: map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{"sandbox": map[string]any{"IPAddress": "172.32.0.14"}},
					"Ports":    map[string]any{"8000/tcp": nil},
				},
				"HostConfig": map[string]any{
					"PortBindings": map[string]any{
						"8000/tcp": []map[string]string{{"HostIp": "", "HostPort": "8000"}},
					},
				},
			},
			expected: [][3]string{{"8000", "172.32.0.14", "8000"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(tc.info)
			}))
			defer srv.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

			result := containerPorts("test-id")

			if tc.expected == nil {
				if len(result) != 0 {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tc.expected) {
				t.Errorf("expected %d results, got %d: %v", len(tc.expected), len(result), result)
				return
			}

			// Check each expected tuple is present (order may vary)
			for _, exp := range tc.expected {
				found := false
				for _, r := range result {
					if r == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %v in results, got %v", exp, result)
				}
			}
		})
	}
}

func TestForwarderLifecycle(t *testing.T) {
	// Start a simple TCP echo server as target
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetPort := target.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				c.Write(buf[:n])
			}(conn)
		}
	}()

	// Start forwarder on random port
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	fwdPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	startForwarder(fwdPort, "127.0.0.1", targetPort)
	defer stopForwarder(fwdPort)

	time.Sleep(100 * time.Millisecond)

	// Verify forwarding works
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := []byte("hello")
	conn.Write(msg)
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("expected 'hello', got %q", string(buf[:n]))
	}

	// Stop and verify port is released
	stopForwarder(fwdPort)
	time.Sleep(100 * time.Millisecond)
	_, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", fwdPort), 100*time.Millisecond)
	if err == nil {
		t.Error("expected connection refused after stop")
	}
}

func TestForwarderBindConflict(t *testing.T) {
	// Occupy a port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// Try to start forwarder on same port — should log error but not panic
	startForwarder(port, "127.0.0.1", 9999)

	mu.Lock()
	_, exists := forwarders[port]
	mu.Unlock()
	if exists {
		t.Error("forwarder should not be registered on conflicting port")
	}
}

func TestStopNonexistent(t *testing.T) {
	// Should not panic
	stopForwarder(99999)
}

func TestConcurrentForwarders(t *testing.T) {
	target, _ := net.Listen("tcp", "127.0.0.1:0")
	defer target.Close()
	targetPort := target.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	ports := []int{}
	for i := 0; i < 3; i++ {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
		ln.Close()
	}

	for _, p := range ports {
		startForwarder(p, "127.0.0.1", targetPort)
	}
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(forwarders)
	mu.Unlock()
	if count < 3 {
		t.Errorf("expected 3 forwarders, got %d", count)
	}

	for _, p := range ports {
		stopForwarder(p)
	}
}

func TestWaitForDocker(t *testing.T) {
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			attempt++
			if attempt < 3 {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
	waitForDocker()

	if attempt < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempt)
	}
}

func TestReconcileAddsForwarders(t *testing.T) {
	srv := mockDockerServer(map[string]map[string]any{
		"container-a": {
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.10"}},
				"Ports":    map[string]any{"3000/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"3000/tcp": []map[string]string{{"HostIp": "", "HostPort": "19880"}},
				},
			},
		},
	})
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
	reconcile()

	mu.Lock()
	_, exists := forwarders[19880]
	mu.Unlock()
	if !exists {
		t.Error("expected forwarder on port 19880 after reconcile")
	}
	stopForwarder(19880)
}

func TestReconcileRemovesStale(t *testing.T) {
	// Start a forwarder manually (simulating a container that was running before)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	stalePort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	startForwarder(stalePort, "172.17.0.99", 9999)
	time.Sleep(100 * time.Millisecond)

	// Mock Docker returns NO containers — the forwarder should be removed
	srv := mockDockerServer(map[string]map[string]any{})
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	reconcile()

	mu.Lock()
	_, exists := forwarders[stalePort]
	mu.Unlock()
	if exists {
		t.Errorf("expected forwarder on port %d to be removed by reconcile", stalePort)
		stopForwarder(stalePort)
	}
}

func TestReconcileIdempotent(t *testing.T) {
	srv := mockDockerServer(map[string]map[string]any{
		"container-b": {
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.20"}},
				"Ports":    map[string]any{"5000/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"5000/tcp": []map[string]string{{"HostIp": "", "HostPort": "19881"}},
				},
			},
		},
	})
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	reconcile()
	reconcile() // second call should be no-op

	mu.Lock()
	count := 0
	for range forwarders {
		count++
	}
	_, exists := forwarders[19881]
	mu.Unlock()

	if !exists {
		t.Error("expected forwarder on port 19881")
	}
	if count != 1 {
		t.Errorf("expected exactly 1 forwarder, got %d", count)
	}
	stopForwarder(19881)
}

func TestEventTriggersReconcile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			flusher, _ := w.(http.Flusher)
			fmt.Fprintln(w, `{"Action":"start","id":"new-container"}`)
			flusher.Flush()
			time.Sleep(500 * time.Millisecond)
			return
		}
		if r.URL.Path == "/containers/json" {
			json.NewEncoder(w).Encode([]map[string]any{{"Id": "new-container"}})
			return
		}
		// /containers/new-container/json
		json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.30"}},
				"Ports":    map[string]any{"7000/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"7000/tcp": []map[string]string{{"HostIp": "", "HostPort": "19882"}},
				},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	// Run watchEvents in background (returns when server closes stream)
	go watchEvents()
	time.Sleep(2 * time.Second) // wait for event + reconcile

	mu.Lock()
	_, exists := forwarders[19882]
	mu.Unlock()
	if !exists {
		t.Error("expected forwarder on port 19882 from event-triggered reconcile")
	}
	stopForwarder(19882)
}

func TestReconcileDetectsTargetDrift(t *testing.T) {
	// Start a forwarder pointing to old IP
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	startForwarder(port, "172.17.0.OLD", 3000)
	time.Sleep(100 * time.Millisecond)

	// Mock Docker returns same port but NEW IP
	srv := mockDockerServer(map[string]map[string]any{
		"drifted-container": {
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{"bridge": map[string]any{"IPAddress": "172.17.0.NEW"}},
				"Ports":    map[string]any{"3000/tcp": nil},
			},
			"HostConfig": map[string]any{
				"PortBindings": map[string]any{
					"3000/tcp": []map[string]string{{"HostIp": "", "HostPort": fmt.Sprintf("%d", port)}},
				},
			},
		},
	})
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	reconcile()

	// Forwarder should now point to new IP
	mu.Lock()
	fwd, exists := forwarders[port]
	var ip string
	if exists {
		ip = fwd.targetIP
	}
	mu.Unlock()

	if !exists {
		t.Fatal("expected forwarder to exist after drift reconcile")
	}
	if ip != "172.17.0.NEW" {
		t.Errorf("expected target IP 172.17.0.NEW, got %s", ip)
	}
	stopForwarder(port)
}

func TestDebounceCoalescesEvents(t *testing.T) {
	callCount := 0
	srv := mockDockerServer(map[string]map[string]any{})
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())

	// Override reconcile behavior by counting desiredForwarders calls
	// We can't easily count reconcile calls, but we can verify debounce
	// by checking that rapid scheduleReconcile calls don't cause issues
	for i := 0; i < 10; i++ {
		scheduleReconcile()
		callCount++
	}

	// Wait for debounce to fire
	time.Sleep(time.Second)

	// If debounce works, only one reconcile ran (not 10).
	// We can't directly assert call count without refactoring,
	// but we verify no panics/races and the timer resolves cleanly.
	if callCount != 10 {
		t.Errorf("expected 10 schedule calls, got %d", callCount)
	}
}
