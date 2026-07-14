// PKCE helpers and file-based pending flow storage.
// Shared between login.ts and callback.ts via file system (no in-memory state).

/// <reference path="../../../gateway/types/gateway.d.ts" />

export function generateCodeVerifier(): string {
  const bytes = gw.crypto.randomBytes(32);
  return gw.crypto.base64url.encode(String.fromCharCode(...bytes));
}

export function codeChallengeS256(verifier: string): string {
  return gw.crypto.sha256(verifier, "base64url");
}

export function generateState(): string {
  const bytes = gw.crypto.randomBytes(16);
  return gw.crypto.base64url.encode(String.fromCharCode(...bytes));
}

export interface PendingFlow {
  provider: string;
  code_verifier: string;
  redirect_uri: string;
  expires_at: number;
}

const PENDING_DIR = "pending";
const FLOW_TTL_MS = 10 * 60 * 1000; // 10 minutes

export function storePendingFlow(state: string, flow: PendingFlow): void {
  const data = JSON.stringify(flow);
  gw.fs.write(`${PENDING_DIR}/${state}.json`, data);
}

export function consumePendingFlow(state: string): PendingFlow | null {
  const path = `${PENDING_DIR}/${state}.json`;
  let data: string;
  try {
    data = gw.fs.read(path);
  } catch {
    return null;
  }
  // Guard against double-spend: treat empty or sentinel as already consumed
  if (!data || data === "__consumed__") {
    return null;
  }
  // Mark as consumed immediately — narrows the race window vs writing ""
  try {
    gw.fs.write(path, "__consumed__");
  } catch {
    // best effort; second reader will hit the sentinel on the next read
  }
  const flow: PendingFlow = JSON.parse(data);
  if (Date.now() > flow.expires_at) {
    return null;
  }
  return flow;
}

export function hmacState(providersJSON: string, providerName: string): string {
  const key = gw.crypto.sha256(providersJSON);
  const sig = gw.crypto.hmac(key, providerName);
  return sig.substring(0, 16) + ":" + providerName;
}

export function verifyHmacState(providersJSON: string, state: string): string | null {
  const colonIdx = state.indexOf(":");
  if (colonIdx < 0) return null;
  const sig = state.substring(0, colonIdx);
  const providerName = state.substring(colonIdx + 1);
  const key = gw.crypto.sha256(providersJSON);
  const expectedSig = gw.crypto.hmac(key, providerName).substring(0, 16);
  if (sig !== expectedSig) return null;
  return providerName;
}
