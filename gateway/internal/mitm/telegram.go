package mitm

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// TelegramRewriter rewrites Telegram Bot API requests to replace the dummy token
// with the real bot token from the environment.
type TelegramRewriter struct {
	realToken string
}

// NewTelegramRewriter creates a rewriter that replaces any bot token in the URL
// with the real token from TELEGRAM_BOT_TOKEN env var.
func NewTelegramRewriter() (*TelegramRewriter, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN not set")
	}
	return &TelegramRewriter{realToken: token}, nil
}

// RewriteRequest replaces /bot<any-token>/ with /bot<real-token>/ in the URL path.
func (r *TelegramRewriter) RewriteRequest(req *http.Request) bool {
	path := req.URL.Path
	if !strings.HasPrefix(path, "/bot") {
		return false
	}

	// Path format: /bot<token>/<method>
	// Find the token portion (between /bot and the next /)
	rest := path[4:] // strip "/bot"
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return false
	}

	// Replace token with real token
	method := rest[slashIdx:] // e.g., "/getUpdates"
	req.URL.Path = "/bot" + r.realToken + method
	return true
}
