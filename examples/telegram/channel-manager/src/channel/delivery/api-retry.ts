/**
 * Retry wrapper for Telegram Bot API calls.
 * Handles 429 rate limits, transient network errors, and bot-blocked scenarios.
 */
import { createLogger } from "../../logger.js";

const log = createLogger("api-retry");

export interface RetryOptions {
  maxRetries?: number;
  baseDelay?: number;
}

/** Shape of grammY/Telegram API errors with response metadata. */
interface TelegramApiError {
  message?: string;
  code?: string;
  response?: {
    error_code?: number;
    parameters?: { retry_after?: number };
  };
}

export async function withRetry<T>(
  fn: () => Promise<T>,
  options?: RetryOptions,
): Promise<T> {
  const maxRetries = options?.maxRetries ?? 3;
  const baseDelay = options?.baseDelay ?? 1000;

  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      return await fn();
    } catch (err: unknown) {
      const error = err as TelegramApiError;

      // 403 Forbidden (bot blocked) — don't retry
      if (error?.response?.error_code === 403) {
        log.warn({ error: error.message }, "bot blocked or forbidden");
        return undefined as T;
      }

      // 429 Rate Limited — wait Retry-After and retry
      if (error?.response?.error_code === 429) {
        const retryAfter = error.response.parameters?.retry_after ?? 5;
        if (attempt < maxRetries) {
          log.warn(
            { retryAfter, attempt: attempt + 1, maxRetries },
            "rate limited, waiting",
          );
          await sleep(retryAfter * 1000);
          continue;
        }
        throw err;
      }

      // Transient network errors — exponential backoff
      if (isTransientError(error) && attempt < maxRetries) {
        const delay = baseDelay * Math.pow(2, attempt);
        log.warn(
          { delay, error: error.message, attempt: attempt + 1, maxRetries },
          "transient error, retrying",
        );
        await sleep(delay);
        continue;
      }

      throw err;
    }
  }

  throw new Error("withRetry: exhausted retries");
}

function isTransientError(err: TelegramApiError): boolean {
  const code = err?.code;
  if (
    code === "ECONNRESET" ||
    code === "ETIMEDOUT" ||
    code === "ENOTFOUND" ||
    code === "EPIPE" ||
    code === "EAI_AGAIN"
  ) {
    return true;
  }
  const msg = err?.message || "";
  if (
    msg.includes("ECONNRESET") ||
    msg.includes("ETIMEDOUT") ||
    msg.includes("socket hang up")
  ) {
    return true;
  }
  return false;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
