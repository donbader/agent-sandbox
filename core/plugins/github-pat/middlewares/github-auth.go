package custom

import (
	"encoding/base64"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
	// The secret is baked at generate-time from plugin options.
	token := "{{ .options.token }}"
	if token != "" {
		gateway.RegisterSecret(token)
	}

	gateway.RegisterMiddleware("github-basic-auth", func(ctx *gateway.MiddlewareContext) error {
		// Git uses Basic auth with format: x-access-token:<PAT>
		if token == "" {
			return nil
		}

		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		ctx.Request.Header.Set("Authorization", "Basic "+basic)
		return nil
	})
}
