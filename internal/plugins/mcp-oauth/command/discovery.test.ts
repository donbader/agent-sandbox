import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { discoverAuthServer } from "./discovery.js";

const mockFetch = vi.fn();
const mockLog = {
  debug: vi.fn(),
  info: vi.fn(),
  warn: vi.fn(),
  error: vi.fn(),
  child: vi.fn(() => mockLog),
};

beforeEach(() => {
  vi.stubGlobal("fetch", mockFetch);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("discoverAuthServer", () => {
  it("fetches well-known URL and returns metadata", async () => {
    const metadata = {
      issuer: "https://auth.example.com",
      authorization_endpoint: "https://auth.example.com/authorize",
      token_endpoint: "https://auth.example.com/token",
      registration_endpoint: "https://auth.example.com/register",
      scopes_supported: ["read", "write"],
    };

    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => metadata,
    });

    const result = await discoverAuthServer("https://mcp.example.com/v1/sse", mockLog);

    expect(mockFetch).toHaveBeenCalledWith(
      "https://mcp.example.com/.well-known/oauth-authorization-server",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(result).toEqual(metadata);
  });

  it("throws on non-200 response", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 404,
      statusText: "Not Found",
    });

    await expect(discoverAuthServer("https://mcp.example.com/v1", mockLog)).rejects.toThrow(
      "OAuth discovery failed for https://mcp.example.com/.well-known/oauth-authorization-server: HTTP 404 Not Found",
    );
  });

  it("throws when metadata is missing required fields", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({ issuer: "https://auth.example.com" }),
    });

    await expect(discoverAuthServer("https://mcp.example.com/api", mockLog)).rejects.toThrow(
      "missing authorization_endpoint or token_endpoint",
    );
  });

  it("passes an AbortSignal for timeout", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({
        issuer: "https://auth.example.com",
        authorization_endpoint: "https://auth.example.com/authorize",
        token_endpoint: "https://auth.example.com/token",
      }),
    });

    await discoverAuthServer("https://mcp.example.com/mcp", mockLog);

    const options = mockFetch.mock.calls[0][1];
    expect(options.signal).toBeInstanceOf(AbortSignal);
  });

  it("uses origin of the MCP URL for well-known path", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({
        issuer: "https://mcp.example.com",
        authorization_endpoint: "https://mcp.example.com/auth",
        token_endpoint: "https://mcp.example.com/token",
      }),
    });

    await discoverAuthServer("https://mcp.example.com:8443/some/deep/path?query=1", mockLog);

    expect(mockFetch).toHaveBeenCalledWith(
      "https://mcp.example.com:8443/.well-known/oauth-authorization-server",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });
});
