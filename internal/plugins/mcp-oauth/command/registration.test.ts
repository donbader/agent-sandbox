import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { registerClient } from "./registration.js";

describe("registerClient", () => {
  const mockFetch = vi.fn();
  const mockLog = {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
    child: vi.fn(() => mockLog),
  };

  beforeEach(() => {
    mockFetch.mockClear();
    mockLog.debug.mockClear();
    vi.stubGlobal("fetch", mockFetch);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registers a client and returns client_id", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        client_id: "new-client-id",
        client_secret: "new-secret",
      }),
    });

    const result = await registerClient(
      "https://auth.example.com/register",
      "http://localhost:3000/oauth/callback",
      mockLog,
      "my-agent",
    );

    expect(result.client_id).toBe("new-client-id");
    expect(result.client_secret).toBe("new-secret");

    expect(mockFetch).toHaveBeenCalledWith(
      "https://auth.example.com/register",
      expect.objectContaining({
        method: "POST",
        headers: { "Content-Type": "application/json" },
      }),
    );

    const body = JSON.parse(mockFetch.mock.calls[0][1].body);
    expect(body.redirect_uris).toEqual(["http://localhost:3000/oauth/callback"]);
    expect(body.grant_types).toEqual(["authorization_code"]);
    expect(body.token_endpoint_auth_method).toBe("none");
    expect(body.client_name).toBe("my-agent");
  });

  it("works without client_name", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ client_id: "cid" }),
    });

    const result = await registerClient(
      "https://auth.example.com/register",
      "http://localhost:3000/callback",
      mockLog,
    );

    expect(result.client_id).toBe("cid");

    const body = JSON.parse(mockFetch.mock.calls[0][1].body);
    expect(body.client_name).toBeUndefined();
  });

  it("throws on non-200 response", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 400,
      text: async () => "invalid request",
    });

    await expect(
      registerClient("https://auth.example.com/register", "http://localhost:3000/callback", mockLog),
    ).rejects.toThrow("Dynamic client registration failed (HTTP 400)");
  });

  it("throws if response missing client_id", async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({}),
    });

    await expect(
      registerClient("https://auth.example.com/register", "http://localhost:3000/callback", mockLog),
    ).rejects.toThrow("Registration response missing client_id");
  });
});
