package jsruntime

//go:generate go run ../../../../cmd/gen-gateway-types

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// HostAPIConfig configures the host APIs injected into JS VMs.
type HostAPIConfig struct {
	DataDir           string   // Base directory for file I/O (plugin-scoped)
	AllowPrivateIPs   bool     // Allow HTTP fetch to private IPs (testing only)
	RegisteredSecrets []string // Secrets registered by the plugin (collected by host)
}

// InjectHostAPIs injects the `gw` global object into a VM with all host APIs.
func InjectHostAPIs(vm *VM, cfg *HostAPIConfig) {
	rt := vm.Runtime()

	cryptoObj := rt.NewObject()
	// @ts-method gw.crypto.sha256(data: string, encoding?: "hex" | "base64url" | "base64"): string
	_ = cryptoObj.Set("sha256", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0).String()
		h := sha256.Sum256([]byte(data))
		// Optional second argument: encoding ("hex" default, "base64url", "base64")
		encoding := "hex"
		if len(call.Arguments) > 1 {
			encoding = call.Argument(1).String()
		}
		switch encoding {
		case "base64url":
			return rt.ToValue(base64.RawURLEncoding.EncodeToString(h[:]))
		case "base64":
			return rt.ToValue(base64.StdEncoding.EncodeToString(h[:]))
		default:
			return rt.ToValue(hex.EncodeToString(h[:]))
		}
	})
	// @ts-method gw.crypto.hmac(key: string, data: string): string
	_ = cryptoObj.Set("hmac", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		data := call.Argument(1).String()
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		return rt.ToValue(hex.EncodeToString(mac.Sum(nil)))
	})
	// @ts-method gw.crypto.randomBytes(n: number): Uint8Array
	_ = cryptoObj.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		n := int(call.Argument(0).ToInteger())
		if n <= 0 || n > 1024*1024 { // Cap at 1MB
			panic(rt.NewGoError(fmt.Errorf("randomBytes: size must be 1-%d, got %d", 1024*1024, n)))
		}
		b := make([]byte, n)
		_, _ = rand.Read(b)
		return rt.ToValue(b)
	})

	base64urlObj := rt.NewObject()
	// @ts-method gw.crypto.base64url.encode(data: string): string
	_ = base64urlObj.Set("encode", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0).String()
		return rt.ToValue(base64.RawURLEncoding.EncodeToString([]byte(data)))
	})
	// @ts-method gw.crypto.base64url.decode(encoded: string): string
	_ = base64urlObj.Set("decode", func(call goja.FunctionCall) goja.Value {
		encoded := call.Argument(0).String()
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("base64url decode: %w", err)))
		}
		return rt.ToValue(string(decoded))
	})
	// @ts-skip (structural wiring)
	_ = cryptoObj.Set("base64url", base64urlObj)

	base64Obj := rt.NewObject()
	// @ts-method gw.crypto.base64.encode(data: string): string
	_ = base64Obj.Set("encode", func(call goja.FunctionCall) goja.Value {
		data := call.Argument(0).String()
		return rt.ToValue(base64.StdEncoding.EncodeToString([]byte(data)))
	})
	// @ts-method gw.crypto.base64.decode(encoded: string): string
	_ = base64Obj.Set("decode", func(call goja.FunctionCall) goja.Value {
		encoded := call.Argument(0).String()
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			panic(rt.NewGoError(fmt.Errorf("base64 decode: %w", err)))
		}
		return rt.ToValue(string(decoded))
	})
	// @ts-skip (structural wiring)
	_ = cryptoObj.Set("base64", base64Obj)

	// File I/O (scoped to DataDir)
	fsObj := rt.NewObject()
	// @ts-method gw.fs.read(path: string): string
	_ = fsObj.Set("read", func(call goja.FunctionCall) goja.Value {
		relPath := call.Argument(0).String()
		absPath, err := safeJoin(cfg.DataDir, relPath)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return rt.ToValue(string(data))
	})
	// @ts-method gw.fs.write(path: string, content: string): void
	_ = fsObj.Set("write", func(call goja.FunctionCall) goja.Value {
		relPath := call.Argument(0).String()
		content := call.Argument(1).String()
		absPath, err := safeJoin(cfg.DataDir, relPath)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		if err := os.MkdirAll(filepath.Dir(absPath), 0700); err != nil {
			panic(rt.NewGoError(err))
		}
		if err := os.WriteFile(absPath, []byte(content), 0600); err != nil {
			panic(rt.NewGoError(err))
		}
		return goja.Undefined()
	})

	// HTTP client (synchronous, SSRF-safe by default)
	httpObj := rt.NewObject()
	// @ts-method gw.http.fetch(url: string, opts?: { method?: string; body?: string; headers?: Record<string, string> }): { status: number; headers: Record<string, string>; body: string }
	_ = httpObj.Set("fetch", func(call goja.FunctionCall) goja.Value {
		urlStr := call.Argument(0).String()
		method := "GET"
		var bodyStr string
		var headers map[string]string

		if len(call.Arguments) > 1 {
			optsVal := call.Argument(1).Export()
			if m, ok := optsVal.(map[string]any); ok {
				if v, ok := m["method"].(string); ok {
					method = v
				}
				if v, ok := m["body"].(string); ok {
					bodyStr = v
				}
				if v, ok := m["headers"].(map[string]any); ok {
					headers = make(map[string]string)
					for k, val := range v {
						headers[k] = fmt.Sprintf("%v", val)
					}
				}
			}
		}

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequest(method, urlStr, bodyReader)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if bodyStr != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		client := &http.Client{
			Timeout:   30 * time.Second,
			Transport: ssrfSafeTransport(cfg.AllowPrivateIPs),
		}
		resp, err := client.Do(req)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			panic(rt.NewGoError(err))
		}

		respHeaders := make(map[string]string)
		for k, v := range resp.Header {
			if len(v) > 0 {
				respHeaders[k] = v[0]
			}
		}

		return rt.ToValue(map[string]any{
			"status":  resp.StatusCode,
			"headers": respHeaders,
			"body":    string(body),
		})
	})

	// Secrets
	secretsObj := rt.NewObject()
	// @ts-method gw.secrets.register(secret: string): void
	_ = secretsObj.Set("register", func(call goja.FunctionCall) goja.Value {
		secret := call.Argument(0).String()
		if secret != "" {
			cfg.RegisteredSecrets = append(cfg.RegisteredSecrets, secret)
		}
		return goja.Undefined()
	})

	// Logging
	logObj := rt.NewObject()
	// @ts-method gw.log.info(msg: string): void
	_ = logObj.Set("info", func(call goja.FunctionCall) goja.Value {
		msg := call.Argument(0).String()
		slog.Info("plugin: " + msg)
		return goja.Undefined()
	})
	// @ts-method gw.log.error(msg: string): void
	_ = logObj.Set("error", func(call goja.FunctionCall) goja.Value {
		msg := call.Argument(0).String()
		slog.Error("plugin: " + msg)
		return goja.Undefined()
	})
	// @ts-method gw.log.debug(msg: string): void
	_ = logObj.Set("debug", func(call goja.FunctionCall) goja.Value {
		msg := call.Argument(0).String()
		slog.Debug("plugin: " + msg)
		return goja.Undefined()
	})

	// Assemble gw object
	// @ts-skip (structural wiring)
	gwObj := rt.NewObject()
	// @ts-skip (structural wiring)
	_ = gwObj.Set("crypto", cryptoObj)
	// @ts-skip (structural wiring)
	_ = gwObj.Set("fs", fsObj)
	// @ts-skip (structural wiring)
	_ = gwObj.Set("http", httpObj)
	// @ts-skip (structural wiring)
	_ = gwObj.Set("secrets", secretsObj)
	// @ts-skip (structural wiring)
	_ = gwObj.Set("log", logObj)
	_ = vm.Set("gw", gwObj)
}

// ssrfSafeTransport returns an http.Transport that blocks requests to private IPs.
func ssrfSafeTransport(allowPrivate bool) *http.Transport {
	if allowPrivate {
		return &http.Transport{}
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", addr, err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
					return nil, fmt.Errorf("refusing to connect to private/loopback IP %s for host %s", ip.IP, host)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}

// safeJoin joins base and rel, rejecting path traversal.
func safeJoin(base, rel string) (string, error) {
	abs := filepath.Join(base, rel)
	cleaned := filepath.Clean(abs)
	baseClean := filepath.Clean(base)
	if !strings.HasPrefix(cleaned, baseClean+string(filepath.Separator)) && cleaned != baseClean {
		return "", fmt.Errorf("path traversal: %q escapes base %q", rel, base)
	}
	return cleaned, nil
}
