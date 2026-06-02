/**
 * PKCE helpers for OAuth 2.0 authorization code flow.
 * Uses Node.js built-in crypto — no external dependencies.
 */
import { randomBytes, createHash } from "node:crypto";

const VERIFIER_CHARSET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~";

/**
 * Generate a random code verifier (43-128 URL-safe characters).
 */
export function generateCodeVerifier(length = 64): string {
  const bytes = randomBytes(length);
  let verifier = "";
  for (let i = 0; i < length; i++) {
    verifier += VERIFIER_CHARSET[bytes[i] % VERIFIER_CHARSET.length];
  }
  return verifier;
}

/**
 * Generate a code challenge from a verifier using SHA-256 + base64url.
 */
export async function generateCodeChallenge(verifier: string): Promise<string> {
  const hash = createHash("sha256").update(verifier).digest();
  return hash.toString("base64url");
}

/**
 * Generate a random state parameter for CSRF protection.
 */
export function generateState(): string {
  return randomBytes(32).toString("base64url");
}
