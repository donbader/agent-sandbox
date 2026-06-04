package custom

import (
	"encoding/base64"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
	gateway.RegisterMiddleware("github-basic-auth", func(ctx *gateway.MiddlewareContext) error {
		// Git uses Basic auth with format: x-access-token:<PAT>
		// The gateway intercepts requests to github.com and injects the real token.
		token := ctx.Env("GITHUB_PAT")
		if token == "" {
			return nil
		}

		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		ctx.Request.Header.Set("Authorization", "Basic "+basic)
		return nil
	})
}
