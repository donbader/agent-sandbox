// Port-forward daemon: reconciles localhost:PORT → container:PORT forwarders.
//
// Uses a controller pattern: one reconcile() function computes desired state
// (running containers with port bindings) and converges actual state (active
// forwarders) to match. Reconcile is triggered by: startup, Docker events
// (debounced), and a periodic safety-net timer.
//
// Solves: agent-browser blocks private IPs but allows localhost.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// forwarder tracks a running TCP forwarder.
type forwarder struct {
	listener   net.Listener
	done       chan struct{}
	targetIP   string
	targetPort int
}

// target represents a desired forwarding destination.
type target struct {
	ip   string
	port int
}

var (
	mu         sync.Mutex
	forwarders = map[int]*forwarder{} // host_port → forwarder

	// Debounce: coalesce rapid events into one reconcile call
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
)

const debounceDelay = 500 * time.Millisecond

func main() {
	log.SetFlags(log.Ltime)
	log.Println("[port-forward] starting daemon")

	waitForDocker()
	reconcile()

	// Periodic reconcile as safety net
	go func() {
		for {
			time.Sleep(30 * time.Second)
			reconcile()
		}
	}()

	// Event-driven reconcile (retries forever)
	for {
		if err := watchEvents(); err != nil {
			log.Printf("[port-forward] event stream error: %v, retrying...", err)
			time.Sleep(2 * time.Second)
		}
	}
}

// scheduleReconcile debounces reconcile calls. Multiple rapid events
// (e.g. 10 containers starting) collapse into one reconcile after the
// debounce delay.
func scheduleReconcile() {
	debounceMu.Lock()
	defer debounceMu.Unlock()
	if debounceTimer != nil {
		debounceTimer.Stop()
	}
	debounceTimer = time.AfterFunc(debounceDelay, reconcile)
}

// desiredForwarders queries Docker for all running containers and returns
// a map of hostPort → target representing the desired forwarding state.
func desiredForwarders() map[int]target {
	resp, err := dockerGet("/containers/json")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var containers []struct {
		Id string
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil
	}

	desired := map[int]target{}
	for _, c := range containers {
		ports := containerPorts(c.Id)
		for _, p := range ports {
			hp, _ := strconv.Atoi(p[0])
			cp, _ := strconv.Atoi(p[2])
			if hp > 0 && cp > 0 {
				desired[hp] = target{ip: p[1], port: cp}
			}
		}
	}
	return desired
}

// reconcile converges actual forwarders to match desired state.
// Idempotent: safe to call any number of times.
func reconcile() {
	desired := desiredForwarders()
	if desired == nil {
		return // API unreachable, skip this cycle
	}

	mu.Lock()
	// Collect stale or drifted forwarders
	var toRemove []int
	for port, fwd := range forwarders {
		t, ok := desired[port]
		if !ok {
			// Port no longer desired
			toRemove = append(toRemove, port)
		} else if fwd.targetIP != t.ip || fwd.targetPort != t.port {
			// Target drifted (container restarted with new IP)
			toRemove = append(toRemove, port)
		}
	}
	// Collect missing forwarders (desired but not running, or about to be removed due to drift)
	removeSet := map[int]bool{}
	for _, p := range toRemove {
		removeSet[p] = true
	}
	var toAdd []int
	for port := range desired {
		if _, ok := forwarders[port]; !ok || removeSet[port] {
			toAdd = append(toAdd, port)
		}
	}
	mu.Unlock()

	// Remove stale/drifted first, then add — order matters for drift case
	for _, port := range toRemove {
		stopForwarder(port)
	}
	for _, port := range toAdd {
		t := desired[port]
		startForwarder(port, t.ip, t.port)
	}
}

// watchEvents streams Docker events and triggers debounced reconcile.
func watchEvents() error {
	resp, err := dockerGet(`/events?filters={"type":["container"],"event":["start","stop","die"]}`)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var event struct {
			Action string
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Action {
		case "start", "stop", "die":
			scheduleReconcile()
		}
	}
	return scanner.Err()
}

// waitForDocker retries until the Docker API responds successfully.
func waitForDocker() {
	for attempt := 0; attempt < 60; attempt++ {
		resp, err := dockerGet("/_ping")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			log.Println("[port-forward] Docker API ready")
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(time.Second)
	}
	log.Println("[port-forward] warning: Docker API not reachable after 60s, proceeding anyway")
}

// dockerGet makes a GET request to the Docker API.
func dockerGet(path string) (*http.Response, error) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "tcp://127.0.0.1:2375"
	}
	addr := strings.TrimPrefix(host, "tcp://")
	url := fmt.Sprintf("http://%s%s", addr, path)
	return http.Get(url)
}

// containerPorts returns (hostPort, containerIP, containerPort) tuples for a container.
func containerPorts(id string) [][3]string {
	resp, err := dockerGet("/containers/" + id + "/json")
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()

	var info struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string
			}
			Ports map[string][]struct {
				HostPort string
			}
		}
		HostConfig struct {
			PortBindings map[string][]struct {
				HostPort string
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil
	}

	// Find container IP (first non-empty)
	var ip string
	for _, net := range info.NetworkSettings.Networks {
		if net.IPAddress != "" {
			ip = net.IPAddress
			break
		}
	}
	if ip == "" {
		return nil
	}

	// Extract port bindings — try NetworkSettings.Ports first, fall back to HostConfig.PortBindings
	var result [][3]string
	for containerPortProto, bindings := range info.NetworkSettings.Ports {
		if bindings == nil {
			continue
		}
		containerPort := strings.Split(containerPortProto, "/")[0]
		for _, b := range bindings {
			if b.HostPort != "" {
				result = append(result, [3]string{b.HostPort, ip, containerPort})
			}
		}
	}

	// Fallback: if NetworkSettings.Ports had no active bindings, check HostConfig.PortBindings
	if len(result) == 0 {
		for containerPortProto, bindings := range info.HostConfig.PortBindings {
			if bindings == nil {
				continue
			}
			containerPort := strings.Split(containerPortProto, "/")[0]
			for _, b := range bindings {
				if b.HostPort != "" {
					result = append(result, [3]string{b.HostPort, ip, containerPort})
				}
			}
		}
	}

	return result
}

// startForwarder starts a TCP forwarder on localhost:hostPort → targetIP:targetPort.
func startForwarder(hostPort int, targetIP string, targetPort int) {
	mu.Lock()
	if _, exists := forwarders[hostPort]; exists {
		mu.Unlock()
		return
	}
	mu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort))
	if err != nil {
		log.Printf("[port-forward] cannot bind :%d: %v", hostPort, err)
		return
	}

	fwd := &forwarder{listener: ln, done: make(chan struct{}), targetIP: targetIP, targetPort: targetPort}

	// Re-check under lock after listen (another goroutine may have raced)
	mu.Lock()
	if _, exists := forwarders[hostPort]; exists {
		mu.Unlock()
		ln.Close()
		return
	}
	forwarders[hostPort] = fwd
	mu.Unlock()

	log.Printf("[port-forward] localhost:%d → %s:%d", hostPort, targetIP, targetPort)

	go func() {
		defer ln.Close()
		for {
			select {
			case <-fwd.done:
				return
			default:
			}
			ln.(*net.TCPListener).SetDeadline(time.Now().Add(1 * time.Second))
			conn, err := ln.Accept()
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			go forward(conn, targetIP, targetPort)
		}
	}()
}

// forward handles a single TCP connection.
func forward(client net.Conn, targetIP string, targetPort int) {
	remote, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetIP, targetPort), 5*time.Second)
	if err != nil {
		client.Close()
		return
	}
	go func() { io.Copy(remote, client); remote.Close() }()
	go func() { io.Copy(client, remote); client.Close() }()
}

// stopForwarder stops the forwarder on the given port.
func stopForwarder(hostPort int) {
	mu.Lock()
	fwd, exists := forwarders[hostPort]
	if exists {
		delete(forwarders, hostPort)
	}
	mu.Unlock()
	if exists {
		close(fwd.done)
		fwd.listener.Close()
		log.Printf("[port-forward] stopped :%d", hostPort)
	}
}
