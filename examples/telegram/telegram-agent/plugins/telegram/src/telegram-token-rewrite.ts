declare const gw: any;

// Rewrites Telegram API request paths to inject the real bot token.
// The agent sees https://api.telegram.org/bot<PLACEHOLDER>/method but this
// middleware replaces the path prefix with the actual token at proxy time.
export default function(ctx: any, options: any) {
  const token = options.bot_token;
  if (!token) return;

  const path = ctx.request.path;
  // Replace /bot<anything>/ with /bot<real-token>/
  const rewritten = path.replace(/^\/bot[^/]*\//, "/bot" + token + "/");
  if (rewritten !== path) {
    ctx.request.setPath(rewritten);
  }

  gw.secrets.register(token);
}
