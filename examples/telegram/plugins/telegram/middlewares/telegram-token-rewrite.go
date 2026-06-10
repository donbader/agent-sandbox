//go:build ignore

// Handler body for telegram-token-rewrite middleware.
// Template variables: {{ .options.bot_token }}
// Available in scope: ctx *gateway.MiddlewareContext, strings (imported)

realToken := "{{ .options.bot_token }}"
if realToken != "" {
	gateway.RegisterSecret(realToken)
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
