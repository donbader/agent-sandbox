/**
 * OAuth command plugin for channel-manager.
 * Provides /oauth command and paste-back interceptor for OAuth callback URLs.
 */
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import type { CommandPlugin, CommandContext, CommandReply } from "./types.js";
import type { OAuthConfig, OAuthProviderConfig, PendingFlow, StoredToken } from "./types.js";
import { generateCodeVerifier, generateCodeChallenge, generateState } from "./pkce.js";
import { discoverAuthServer } from "./discovery.js";
import { registerClient } from "./registration.js";

const FLOW_TIMEOUT_MS = 10 * 60 * 1000; // 10 minutes
const DEFAULT_TOKEN_DIR = "/data/oauth-tokens";
const DEFAULT_REDIRECT_URI = "http://localhost:3000/oauth/callback";

export class OAuthCommandPlugin implements CommandPlugin {
  name = "mcp-oauth";
  commands: Record<string, (ctx: CommandContext) => Promise<void>>;

  private config: OAuthConfig = { providers: {} };
  private pendingFlows = new Map<string, PendingFlow>();
  private cleanupTimer: ReturnType<typeof setInterval> | null = null;

  constructor() {
    this.commands = {
      oauth: (ctx) => this.handleOauth(ctx),
    };
  }

  init(config: Record<string, unknown>): void {
    const oauthConfig = config["oauth"] as OAuthConfig | undefined;
    if (oauthConfig) {
      this.config = oauthConfig;
    }
    this.cleanupTimer = setInterval(() => this.cleanupStaleFlows(), 60_000);
  }

  destroy(): void {
    if (this.cleanupTimer) {
      clearInterval(this.cleanupTimer);
      this.cleanupTimer = null;
    }
  }

  async onMessage(text: string, chatId: string, reply: CommandReply): Promise<boolean> {
    if (this.pendingFlows.size === 0) return false;
    return this.handleCallbackUrl(text.trim(), chatId, reply);
  }

  private async handleOauth(ctx: CommandContext): Promise<void> {
    if (!ctx.args) {
      ctx.reply(this.listProviders());
      return;
    }

    const providerName = ctx.args.trim();
    const providerConfig = this.config.providers[providerName];
    if (!providerConfig) {
      ctx.reply(`Unknown provider: ${providerName}\nAvailable: ${Object.keys(this.config.providers).join(", ") || "(none)"}`);
      return;
    }

    await this.startFlow(providerName, providerConfig, ctx.chatId, ctx.reply);
  }

  private listProviders(): string {
    const entries = Object.entries(this.config.providers);
    if (entries.length === 0) {
      return "No OAuth providers configured.";
    }

    const lines = ["OAuth providers:"];
    for (const [name, config] of entries) {
      const tokenFile = this.getTokenFile(name, config);
      const status = existsSync(tokenFile) ? "connected" : "not connected";
      lines.push(`  ${name} — ${status}`);
    }
    return lines.join("\n");
  }

  private async startFlow(
    name: string,
    config: OAuthProviderConfig,
    chatId: string,
    reply: CommandReply,
  ): Promise<void> {
    try {
      const metadata = await discoverAuthServer(config.mcp_url);

      let clientId = config.client_id ?? "";
      let clientSecret = config.client_secret;

      // If no client_id configured, use Dynamic Client Registration (RFC 7591).
      if (!clientId) {
        if (!metadata.registration_endpoint) {
          reply(`Error: No client_id configured for "${name}" and server doesn't support dynamic registration.\nAdd client_id to your agent.yaml config.`);
          return;
        }
        reply(`Registering client with ${name}...`);
        const reg = await registerClient(metadata.registration_endpoint, DEFAULT_REDIRECT_URI, `agent-sandbox-${name}`);
        clientId = reg.client_id;
        clientSecret = reg.client_secret;
      }

      const codeVerifier = generateCodeVerifier();
      const codeChallenge = await generateCodeChallenge(codeVerifier);
      const state = generateState();

      const flow: PendingFlow = {
        provider: name,
        chatId,
        codeVerifier,
        state,
        tokenEndpoint: metadata.token_endpoint,
        clientId,
        clientSecret,
        redirectUri: DEFAULT_REDIRECT_URI,
        startedAt: Date.now(),
      };
      this.pendingFlows.set(state, flow);

      const params = new URLSearchParams({
        response_type: "code",
        client_id: clientId,
        redirect_uri: DEFAULT_REDIRECT_URI,
        state,
        code_challenge: codeChallenge,
        code_challenge_method: "S256",
      });

      if (metadata.scopes_supported?.length) {
        params.set("scope", metadata.scopes_supported.join(" "));
      }

      const authUrl = `${metadata.authorization_endpoint}?${params.toString()}`;
      reply(`Authorize with ${name}:\n${authUrl}\n\nAfter authorizing, paste the callback URL here.`);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      reply(`OAuth flow error for ${name}: ${message}`);
    }
  }

  private async handleCallbackUrl(
    text: string,
    chatId: string,
    reply: CommandReply,
  ): Promise<boolean> {
    let url: URL;
    try {
      url = new URL(text);
    } catch {
      return false;
    }

    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    if (!code || !state) return false;

    const flow = this.pendingFlows.get(state);
    if (!flow) return false;
    if (flow.chatId !== chatId) return false;

    this.pendingFlows.delete(state);

    try {
      const token = await this.exchangeCode(code, flow);
      this.writeToken(flow.provider, token);
      reply(`OAuth complete for ${flow.provider}. Token saved.`);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      reply(`Token exchange failed for ${flow.provider}: ${message}`);
    }

    return true;
  }

  private async exchangeCode(code: string, flow: PendingFlow): Promise<StoredToken> {
    const body = new URLSearchParams({
      grant_type: "authorization_code",
      code,
      code_verifier: flow.codeVerifier,
      redirect_uri: flow.redirectUri,
      client_id: flow.clientId,
    });

    if (flow.clientSecret) {
      body.set("client_secret", flow.clientSecret);
    }

    const response = await fetch(flow.tokenEndpoint, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(`Token endpoint returned HTTP ${response.status}: ${text}`);
    }

    const data = (await response.json()) as {
      access_token: string;
      refresh_token?: string;
      expires_in?: number;
    };

    return {
      access_token: data.access_token,
      refresh_token: data.refresh_token,
      expires_at: Math.floor(Date.now() / 1000) + (data.expires_in ?? 3600),
      token_endpoint: flow.tokenEndpoint,
      client_id: flow.clientId,
      client_secret: flow.clientSecret,
    };
  }

  private writeToken(provider: string, token: StoredToken): void {
    const config = this.config.providers[provider];
    const tokenFile = this.getTokenFile(provider, config);
    const dir = dirname(tokenFile);
    mkdirSync(dir, { recursive: true });
    writeFileSync(tokenFile, JSON.stringify(token, null, 2), { mode: 0o600 });
  }

  private getTokenFile(name: string, config?: OAuthProviderConfig): string {
    if (config?.token_file) return config.token_file;
    const tokenDir = this.config.token_dir ?? DEFAULT_TOKEN_DIR;
    return `${tokenDir}/${name}.json`;
  }

  private cleanupStaleFlows(): void {
    const now = Date.now();
    for (const [state, flow] of this.pendingFlows) {
      if (now - flow.startedAt > FLOW_TIMEOUT_MS) {
        this.pendingFlows.delete(state);
      }
    }
  }
}

export function createOAuthPlugin(): CommandPlugin {
  return new OAuthCommandPlugin();
}

export default createOAuthPlugin();
