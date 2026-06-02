import { describe, it, expect } from "vitest";
import { generateCodeVerifier, generateCodeChallenge, generateState } from "./pkce.js";

describe("generateCodeVerifier", () => {
  it("returns a string of the requested length", () => {
    const verifier = generateCodeVerifier(64);
    expect(verifier).toHaveLength(64);
  });

  it("defaults to 64 characters", () => {
    const verifier = generateCodeVerifier();
    expect(verifier).toHaveLength(64);
  });

  it("only contains URL-safe characters", () => {
    const verifier = generateCodeVerifier(128);
    expect(verifier).toMatch(/^[A-Za-z0-9\-._~]+$/);
  });

  it("generates different values each call", () => {
    const a = generateCodeVerifier();
    const b = generateCodeVerifier();
    expect(a).not.toBe(b);
  });
});

describe("generateCodeChallenge", () => {
  it("produces a valid base64url string (no padding)", async () => {
    const verifier = generateCodeVerifier();
    const challenge = await generateCodeChallenge(verifier);
    // base64url: A-Z, a-z, 0-9, -, _ (no +, /, =)
    expect(challenge).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it("produces a 43-char SHA-256 hash for any verifier", async () => {
    // SHA-256 = 32 bytes → base64url = 43 chars (no padding)
    const challenge = await generateCodeChallenge("test-verifier");
    expect(challenge).toHaveLength(43);
  });

  it("produces deterministic output for same input", async () => {
    const a = await generateCodeChallenge("same-input");
    const b = await generateCodeChallenge("same-input");
    expect(a).toBe(b);
  });

  it("produces different output for different input", async () => {
    const a = await generateCodeChallenge("input-a");
    const b = await generateCodeChallenge("input-b");
    expect(a).not.toBe(b);
  });
});

describe("generateState", () => {
  it("returns a non-empty string", () => {
    const state = generateState();
    expect(state.length).toBeGreaterThan(0);
  });

  it("produces base64url-safe characters", () => {
    const state = generateState();
    expect(state).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it("generates unique values", () => {
    const states = new Set(Array.from({ length: 100 }, () => generateState()));
    expect(states.size).toBe(100);
  });
});
