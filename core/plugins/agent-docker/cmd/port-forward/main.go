// Port-forward daemon: watches Docker container starts and forwards localhost:PORT → container:PORT.
//
// Solves: agent-browser blocks private IPs but allows localhost. By forwarding
// localhost:PORT → container_ip:PORT, the agent can access spawned containers.
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
	listener net.Listener
	done     chan struct{}
}

var (
	mu         sync.Mutex
	forwarders = map[int]*forwarder{} // host_port → forwarder
)

func main() {
	log.SetFlags(log.Ltime)
	log.Println("[port-forward] starting daemon")

	// Scan existing containers
	scanExisting()

	// Watch for new events (retries forever)
	for {
		if err := watchEvents(); err != nil {
			log.Printf("[port-forward] event stream error: %v, retrying...", err)
			time.Sleep(2 * time.Second)
		}
	}
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

	// Extract port bindings
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

	fwd := &forwarder{listener: ln, done: make(chan struct{})}
	mu.Lock()
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

// handleStart sets up forwarding for a newly started container.
func handleStart(containerID string) {
	time.Sleep(500 * time.Millisecond) // let networking settle
	ports := containerPorts(containerID)
	for _, p := range ports {
		hp, _ := strconv.Atoi(p[0])
		cp, _ := strconv.Atoi(p[2])
		if hp > 0 && cp > 0 {
			startForwarder(hp, p[1], cp)
		}
	}
}

// handleStop cleans up dead forwarders by probing backends.
func handleStop() {
	mu.Lock()
	ports := make([]int, 0, len(forwarders))
	for p := range forwarders {
		ports = append(ports, p)
	}
	mu.Unlock()

	for _, port := range ports {
		mu.Lock()
		fwd, exists := forwarders[port]
		mu.Unlock()
		if !exists {
			continue
		}
		// Probe: try connecting to the backend through the forwarder
		_ = fwd // We can't easily probe the backend without knowing its IP.
		// Instead, just let dead connections fail naturally.
		// A more robust approach: re-inspect all running containers and remove
		// forwarders whose ports no longer appear.
	}
	pruneDeadForwarders()
}

// pruneDeadForwarders removes forwarders whose backend is unreachable.
func pruneDeadForwarders() {
	// List all running containers' port bindings
	resp, err := dockerGet("/containers/json")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var containers []struct {
		Ports []struct {
			PublicPort int
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return
	}

	activePorts := map[int]bool{}
	for _, c := range containers {
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				activePorts[p.PublicPort] = true
			}
		}
	}

	mu.Lock()
	toRemove := []int{}
	for port := range forwarders {
		if !activePorts[port] {
			toRemove = append(toRemove, port)
		}
	}
	mu.Unlock()

	for _, port := range toRemove {
		stopForwarder(port)
	}
}

// scanExisting sets up forwarding for already-running containers.
func scanExisting() {
	resp, err := dockerGet("/containers/json")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var containers []struct {
		Id string
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return
	}
	for _, c := range containers {
		handleStart(c.Id)
	}
}

// watchEvents streams Docker events and reacts to container start/stop.
func watchEvents() error {
	resp, err := dockerGet("/events?filters=" + `{"type":["container"],"event":["start","stop","die"]}`)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var event struct {
			Action string
			ID     string `json:"id"`
			Actor  struct {
				ID string
			}
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		id := event.ID
		if id == "" {
			id = event.Actor.ID
		}

		switch event.Action {
		case "start":
			go handleStart(id)
		case "stop", "die":
			go handleStop()
		}
	}
	return scanner.Err()
}
