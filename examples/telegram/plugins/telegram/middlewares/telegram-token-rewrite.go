package custom

import (
	"strings"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
	realToken := "{{ .options.bot_token }}"
	if realToken != "" {
		gateway.RegisterSecret(realToken)
	}

	// Domains are baked at generate-time from service URL.
	domains := strings.Split("{{ .domainsList }}", ",")

	gateway.RegisterMiddleware(gateway.MiddlewareDef{
		Name:    "telegram-token-rewrite",
		Domains: domains,
		Func: func(ctx *gateway.MiddlewareContext) error {
			if realToken == "" {
				return nil
			}

			path := ctx.Request.URL.Path
			if idx := strings.Index(path, "/bot"); idx != -1 {
				rest := path[idx+4:]
				if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
					method := rest[slashIdx:]
					ctx.Request.URL.Path = path[:idx] + "/bot" + realToken + method
				}
			}

			return nil
		},
	})
}
