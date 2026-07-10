#!/usr/bin/env python3
"""Port-forward daemon: watches Docker container starts and forwards localhost:PORT → container:PORT.

Runs as a background process inside the agent container. Uses Docker events API
to detect container start/stop and manages TCP forwarders accordingly.

This solves the problem where agent-browser blocks private IPs (172.x.x.x) but
allows localhost. By forwarding localhost:PORT → container_ip:PORT, the agent
can access spawned containers via localhost.
"""
import json
import os
import socket
import subprocess
import sys
import threading
import time
from typing import Dict, Tuple

# Active forwarders: (host_port) → (thread, server_socket)
active: Dict[int, Tuple[threading.Thread, socket.socket]] = {}
lock = threading.Lock()


def get_container_ports(container_id: str) -> list[tuple[int, str, int]]:
    """Get port bindings for a container. Returns [(host_port, container_ip, container_port), ...]"""
    try:
        result = subprocess.run(
            ["docker", "inspect", container_id],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode != 0:
            return []
        
        info = json.loads(result.stdout)[0]
        
        # Get container IP on any network
        networks = info.get("NetworkSettings", {}).get("Networks", {})
        container_ip = None
        for net_name, net_info in networks.items():
            ip = net_info.get("IPAddress")
            if ip:
                container_ip = ip
                break
        
        if not container_ip:
            return []
        
        # Get port bindings
        ports = info.get("NetworkSettings", {}).get("Ports", {}) or {}
        bindings = []
        for container_port_proto, host_bindings in ports.items():
            if not host_bindings:
                continue
            container_port = int(container_port_proto.split("/")[0])
            for binding in host_bindings:
                host_port = int(binding.get("HostPort", 0))
                if host_port > 0:
                    bindings.append((host_port, container_ip, container_port))
        
        return bindings
    except Exception as e:
        print(f"[port-forward] inspect error: {e}", file=sys.stderr, flush=True)
        return []


def tcp_forward(src: socket.socket, dst: socket.socket):
    """Forward data between two sockets."""
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except (OSError, ConnectionError):
        pass
    finally:
        try:
            src.close()
        except OSError:
            pass
        try:
            dst.close()
        except OSError:
            pass


def run_forwarder(host_port: int, container_ip: str, container_port: int):
    """Run a TCP forwarder on localhost:host_port → container_ip:container_port."""
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        server.bind(("127.0.0.1", host_port))
    except OSError as e:
        print(f"[port-forward] cannot bind localhost:{host_port}: {e}", file=sys.stderr, flush=True)
        return
    
    server.listen(16)
    server.settimeout(1.0)  # Allow periodic check for shutdown
    
    with lock:
        active[host_port] = (threading.current_thread(), server)
    
    print(f"[port-forward] localhost:{host_port} → {container_ip}:{container_port}", flush=True)
    
    while True:
        with lock:
            if host_port not in active:
                break
        try:
            client, _ = server.accept()
        except socket.timeout:
            continue
        except OSError:
            break
        
        try:
            remote = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            remote.connect((container_ip, container_port))
            threading.Thread(target=tcp_forward, args=(client, remote), daemon=True).start()
            threading.Thread(target=tcp_forward, args=(remote, client), daemon=True).start()
        except (OSError, ConnectionError) as e:
            client.close()
    
    server.close()
    print(f"[port-forward] stopped localhost:{host_port}", flush=True)


def stop_forwarder(host_port: int):
    """Stop a forwarder for the given host port."""
    with lock:
        entry = active.pop(host_port, None)
    if entry:
        _, server = entry
        try:
            server.close()
        except OSError:
            pass


def handle_start(container_id: str):
    """Handle container start: set up port forwarding."""
    # Small delay to let networking settle
    time.sleep(0.5)
    bindings = get_container_ports(container_id)
    for host_port, container_ip, container_port in bindings:
        with lock:
            if host_port in active:
                continue  # Already forwarding
        t = threading.Thread(
            target=run_forwarder,
            args=(host_port, container_ip, container_port),
            daemon=True
        )
        t.start()


def handle_stop(container_id: str):
    """Handle container stop: tear down port forwarding."""
    # We don't know which ports this container had, so inspect won't work (it's stopped).
    # Instead, check all active forwarders and remove dead ones.
    with lock:
        ports_to_check = list(active.keys())
    
    for port in ports_to_check:
        # Try connecting to see if the backend is still alive
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            s.settimeout(0.5)
            s.connect(("127.0.0.1", port))
            s.close()
        except (OSError, ConnectionError):
            stop_forwarder(port)


def watch_events():
    """Watch Docker events for container start/stop."""
    while True:
        try:
            proc = subprocess.Popen(
                ["docker", "events", "--filter", "type=container",
                 "--filter", "event=start", "--filter", "event=stop",
                 "--filter", "event=die", "--format", "{{json .}}"],
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True
            )
            
            for line in proc.stdout:
                try:
                    event = json.loads(line.strip())
                    action = event.get("Action", "")
                    container_id = event.get("id", event.get("Actor", {}).get("ID", ""))
                    
                    if action == "start" and container_id:
                        threading.Thread(target=handle_start, args=(container_id,), daemon=True).start()
                    elif action in ("stop", "die") and container_id:
                        threading.Thread(target=handle_stop, args=(container_id,), daemon=True).start()
                except (json.JSONDecodeError, KeyError):
                    continue
        except Exception as e:
            print(f"[port-forward] event watch error: {e}, retrying...", file=sys.stderr, flush=True)
            time.sleep(2)


def scan_existing():
    """Scan already-running containers and set up forwarding."""
    try:
        result = subprocess.run(
            ["docker", "ps", "-q"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode == 0:
            for cid in result.stdout.strip().split("\n"):
                if cid:
                    handle_start(cid)
    except Exception:
        pass


if __name__ == "__main__":
    print("[port-forward] starting daemon", flush=True)
    # Forward existing containers
    scan_existing()
    # Watch for new ones
    watch_events()
