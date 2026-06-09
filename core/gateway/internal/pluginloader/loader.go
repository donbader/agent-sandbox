package pluginloader

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/core/gateway/internal/jsruntime"
	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"gopkg.in/yaml.v3"
)

// LoadPluginsFromFile reads a plugins config YAML file and loads all plugins.
func LoadPluginsFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No plugins config = no TS plugins to load
		}
		return fmt.Errorf("read plugins config: %w", err)
	}

	var cfg PluginsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse plugins config: %w", err)
	}

	return LoadPlugins(&cfg)
}

// LoadPlugins loads all plugins from the given config.
func LoadPlugins(cfg *PluginsConfig) error {
	for _, plugin := range cfg.Plugins {
		if err := loadPlugin(plugin); err != nil {
			return fmt.Errorf("plugin %q: %w", plugin.Name, err)
		}
		slog.Info("loaded plugin", "name", plugin.Name,
			"middlewares", len(plugin.Gateway.Middlewares),
			"routes", len(plugin.Gateway.Routes))
	}
	return nil
}

func loadPlugin(plugin PluginConfig) error {
	dataDir := "/data/plugins/" + plugin.Name
	if d, ok := plugin.Options["token_dir"].(string); ok {
		dataDir = d
	}

	// Load middleware handlers
	for _, mw := range plugin.Gateway.Middlewares {
		entryPoint := filepath.Join(plugin.Dir, mw.Script)
		bundled, err := jsruntime.Bundle(entryPoint)
		if err != nil {
			return fmt.Errorf("bundle middleware %s: %w", mw.Script, err)
		}

		domains := mw.Domains
		scriptName := filepath.Base(mw.Script)
		mwName := fmt.Sprintf("ts:%s:%s", plugin.Name, scriptName)
		opts := plugin.Options
		pluginDataDir := dataDir

		gateway.RegisterMiddleware(gateway.MiddlewareDef{
			Name:    mwName,
			Domains: domains,
			Func: func(ctx *gateway.MiddlewareContext) error {
				return execMiddleware(bundled, ctx, opts, pluginDataDir)
			},
		})
	}

	// Load route handlers
	for _, route := range plugin.Gateway.Routes {
		entryPoint := filepath.Join(plugin.Dir, route.Handler)
		bundled, err := jsruntime.Bundle(entryPoint)
		if err != nil {
			return fmt.Errorf("bundle route handler %s: %w", route.Handler, err)
		}

		namespacedPath := "/plugins/" + plugin.Name + normalizePath(route.Path)
		opts := plugin.Options
		pluginDataDir := dataDir

		gateway.RegisterRoute(gateway.RouteDef{
			Path: namespacedPath,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				execRouteHandler(bundled, w, r, opts, pluginDataDir)
			},
		})
	}

	return nil
}

func execMiddleware(bundledJS string, ctx *gateway.MiddlewareContext, opts map[string]any, dataDir string) error {
	vm := jsruntime.NewVM()
	hostCfg := &jsruntime.HostAPIConfig{DataDir: dataDir}
	jsruntime.InjectHostAPIs(vm, hostCfg)

	reqCtx := jsruntime.NewRequestContext(ctx.Request, nil)
	if err := vm.Set("ctx", reqCtx.ToJSObject(vm)); err != nil {
		return fmt.Errorf("set ctx: %w", err)
	}
	if err := vm.Set("options", opts); err != nil {
		return fmt.Errorf("set options: %w", err)
	}

	// Execute: __handler contains {default: function(ctx, options){...}}
	_, err := vm.RunString(bundledJS + "\n__handler.default(ctx, options);")
	if err != nil {
		return fmt.Errorf("exec middleware: %w", err)
	}

	// Propagate abort back to gateway context
	if reqCtx.AbortStatus != 0 {
		ctx.AbortStatus = reqCtx.AbortStatus
		ctx.AbortBody = reqCtx.AbortBody
		ctx.AbortHeaders = reqCtx.AbortHeaders
	}

	// Register any secrets the plugin declared
	for _, s := range hostCfg.RegisteredSecrets {
		gateway.RegisterSecret(s)
	}

	return nil
}

func execRouteHandler(bundledJS string, w http.ResponseWriter, r *http.Request, opts map[string]any, dataDir string) {
	vm := jsruntime.NewVM()
	hostCfg := &jsruntime.HostAPIConfig{DataDir: dataDir}
	jsruntime.InjectHostAPIs(vm, hostCfg)

	reqCtx := jsruntime.NewRequestContext(r, w)
	if err := vm.Set("ctx", reqCtx.ToJSObject(vm)); err != nil {
		slog.Error("plugin route: set ctx", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := vm.Set("options", opts); err != nil {
		slog.Error("plugin route: set options", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_, err := vm.RunString(bundledJS + "\n__handler.default(ctx, options);")
	if err != nil {
		slog.Error("plugin route handler error", "error", err)
		http.Error(w, "plugin error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write response from context
	for k, vals := range reqCtx.ResponseHeaders {
		for _, v := range vals {
			w.Header().Set(k, v)
		}
	}
	if reqCtx.ResponseStatus > 0 {
		w.WriteHeader(reqCtx.ResponseStatus)
	}
	if reqCtx.ResponseBody != "" {
		_, _ = w.Write([]byte(reqCtx.ResponseBody))
	}

	// Register any secrets
	for _, s := range hostCfg.RegisteredSecrets {
		gateway.RegisterSecret(s)
	}
}

func normalizePath(path string) string {
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}
