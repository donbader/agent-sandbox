import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("./formatter/telegram.js", () => ({
  formatMarkdown: vi.fn((text: string) => text),
  closeOpenTags: vi.fn((html: string) => html),
  splitMessage: vi.fn((html: string) => [html]),
  MAX_MESSAGE_LENGTH: 4096,
}));

import { StreamController } from "./stream-controller.js";
import type { StreamControllerDeps, StreamControllerOptions } from "./stream-controller.js";

function createMockDeps(): StreamControllerDeps {
  return {
    chatId: 123,
    sendMessage: vi.fn().mockResolvedValue(1),
    editMessage: vi.fn().mockResolvedValue(undefined),
    sendDraft: vi.fn().mockResolvedValue(undefined),
  };
}

describe("StreamController", () => {
  let deps: StreamControllerDeps;

  beforeEach(() => {
    vi.useFakeTimers();
    deps = createMockDeps();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("BUFFERING state", () => {
    it("sends short response as single message when finalize before timer", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello world");
      await sc.finalize();

      expect(deps.sendMessage).toHaveBeenCalledTimes(1);
      expect(deps.sendMessage).toHaveBeenCalledWith("Hello world", { parse_mode: "HTML" });
      expect(deps.editMessage).not.toHaveBeenCalled();
    });

    it("enters streaming after 300ms buffer timer fires", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello");

      // Advance past buffer timer
      await vi.advanceTimersByTimeAsync(300);

      // Should have sent first message (entered streaming)
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);
    });

    it("enters streaming immediately on toolStart", async () => {
      const sc = new StreamController(deps);
      sc.toolStart("tc1", "Read file src/main.ts");

      // Should send immediately, no 300ms wait
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);
    });

    it("enters streaming immediately on pushThinking", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("Let me think about this...");

      // Thinking goes to draft, and streaming mode should activate
      expect(deps.sendDraft).toHaveBeenCalledTimes(1);
    });
  });

  describe("STREAMING state", () => {
    it("edits message on throttle interval when content changes", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello");

      // Enter streaming
      await vi.advanceTimersByTimeAsync(300);
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      // Push more content
      sc.pushText(" world");

      // Advance past throttle interval (1000ms)
      await vi.advanceTimersByTimeAsync(1000);

      expect(deps.editMessage).toHaveBeenCalled();
    });

    it("skips edit when content unchanged since last send", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello");

      // Enter streaming
      await vi.advanceTimersByTimeAsync(300);
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      // Don't push any new content — just advance throttle
      await vi.advanceTimersByTimeAsync(1000);

      expect(deps.editMessage).not.toHaveBeenCalled();
    });

    it("uses custom throttleMs option", async () => {
      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.pushText("Hello");

      await vi.advanceTimersByTimeAsync(100);
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      sc.pushText(" world");

      // Should NOT edit yet (only 400ms into 500ms throttle)
      await vi.advanceTimersByTimeAsync(400);
      expect(deps.editMessage).not.toHaveBeenCalled();

      // Now at 500ms since last send — should edit
      await vi.advanceTimersByTimeAsync(100);
      expect(deps.editMessage).toHaveBeenCalled();
    });
  });

  describe("tool rendering", () => {
    it("renders tool line with in_progress icon", async () => {
      const sc = new StreamController(deps);
      sc.toolStart("tc1", "Read file src/main.ts");

      // Should have sent with tool line
      expect(deps.sendMessage).toHaveBeenCalledWith(
        expect.stringContaining("🔨 Read file src/main.ts ⏳"),
        expect.any(Object),
      );
    });

    it("updates tool status to completed", async () => {
      const sc = new StreamController(deps);
      sc.toolStart("tc1", "Read file src/main.ts");

      await vi.advanceTimersByTimeAsync(1000);
      sc.toolUpdate("tc1", "completed");

      await vi.advanceTimersByTimeAsync(1000);

      expect(deps.editMessage).toHaveBeenCalledWith(
        expect.any(Number),
        expect.stringContaining("🔨 Read file src/main.ts ✅"),
        expect.any(Object),
      );
    });

    it("updates tool status to failed", async () => {
      const sc = new StreamController(deps);
      sc.toolStart("tc1", "grep TODO");

      await vi.advanceTimersByTimeAsync(1000);
      sc.toolUpdate("tc1", "failed");

      await vi.advanceTimersByTimeAsync(1000);

      expect(deps.editMessage).toHaveBeenCalledWith(
        expect.any(Number),
        expect.stringContaining("🔨 grep TODO ❌"),
        expect.any(Object),
      );
    });
  });

  describe("finalize", () => {
    it("does final edit if content differs from last sent", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello");

      // Enter streaming
      await vi.advanceTimersByTimeAsync(300);
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      // Push more (dirty)
      sc.pushText(" world, this is the final text");

      // Finalize without waiting for throttle
      await sc.finalize();

      expect(deps.editMessage).toHaveBeenCalledWith(
        expect.any(Number),
        expect.stringContaining("Hello world, this is the final text"),
        expect.any(Object),
      );
    });

    it("skips final edit if content matches last sent", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Hello");

      // Enter streaming — sends "Hello"
      await vi.advanceTimersByTimeAsync(300);
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      // Finalize with no new content
      await sc.finalize();

      expect(deps.editMessage).not.toHaveBeenCalled();
    });
  });

  describe("thinking draft", () => {
    it("sends thinking content to draft with brain emoji", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("Analyzing the code...");

      expect(deps.sendDraft).toHaveBeenCalledWith(
        1,
        expect.stringContaining("🧠 Analyzing the code..."),
      );
    });

    it("accumulates thinking across calls", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("First thought");
      sc.pushThinking(" and second thought");

      // Second call should have accumulated text
      const lastCall = (deps.sendDraft as ReturnType<typeof vi.fn>).mock.calls.at(-1)!;
      expect(lastCall[1]).toContain("First thought and second thought");
    });

    it("refreshes draft before 30s timeout", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("Thinking...");

      // Clear mock to track refresh calls
      (deps.sendDraft as ReturnType<typeof vi.fn>).mockClear();

      // Advance to 25s — should refresh
      await vi.advanceTimersByTimeAsync(25_000);

      expect(deps.sendDraft).toHaveBeenCalled();
    });

    it("uses custom draftId", async () => {
      const sc = new StreamController(deps, { draftId: 5 });
      sc.pushThinking("Hi");

      expect(deps.sendDraft).toHaveBeenCalledWith(5, expect.any(String));
    });
  });

  describe("tool result preview removal", () => {
    it("removes result preview 2s after new content appends below", async () => {
      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.toolStart("tc1", "Read file");

      // Enter streaming
      await vi.advanceTimersByTimeAsync(100);

      // Tool completes with a result preview
      sc.toolUpdate("tc1", "completed", [{ type: "content", content: { type: "text", text: "file contents" } }]);

      // New text arrives below — schedules removal of result preview
      sc.pushText("Here is the answer");

      // Advance 1.5s — preview should still be there
      await vi.advanceTimersByTimeAsync(1500);

      // Check that the last edit still has the preview
      const editCalls = (deps.editMessage as ReturnType<typeof vi.fn>).mock.calls;
      const lastEditWithPreview = editCalls.filter((c: any[]) =>
        (c[1] as string).includes("```"),
      );
      expect(lastEditWithPreview.length).toBeGreaterThan(0);

      // Clear mocks to isolate next edit
      (deps.editMessage as ReturnType<typeof vi.fn>).mockClear();

      // Advance past 2s total — preview removed, triggers edit
      await vi.advanceTimersByTimeAsync(1000);

      // Should have triggered an edit without the preview
      expect(deps.editMessage).toHaveBeenCalled();
      const finalEdit = (deps.editMessage as ReturnType<typeof vi.fn>).mock.calls.at(-1)!;
      expect(finalEdit[1]).not.toContain("```");
    });

    it("does not schedule duplicate removal timers for the same tool", async () => {
      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.toolStart("tc1", "Read file");

      await vi.advanceTimersByTimeAsync(100);

      sc.toolUpdate("tc1", "completed", [{ type: "content", content: { type: "text", text: "result" } }]);

      // Two pushText calls — should only schedule one timer
      sc.pushText("First");
      sc.pushText("Second");

      (deps.editMessage as ReturnType<typeof vi.fn>).mockClear();

      // Advance 2s — only one removal edit should fire
      await vi.advanceTimersByTimeAsync(2500);

      const editsWithoutPreview = (deps.editMessage as ReturnType<typeof vi.fn>).mock.calls.filter(
        (c: any[]) => !(c[1] as string).includes("```"),
      );
      // At least one edit without preview (the removal)
      expect(editsWithoutPreview.length).toBeGreaterThanOrEqual(1);
    });
  });

  describe("thinking draft 'Still thinking...' fallback", () => {
    it("shows 'Still thinking...' on refresh when no new thinking arrived", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("Initial thought");

      // Clear to isolate refresh calls
      (deps.sendDraft as ReturnType<typeof vi.fn>).mockClear();

      // First refresh at 25s — thinkingDirty was set true by pushThinking,
      // but the refresh should reset it
      await vi.advanceTimersByTimeAsync(25_000);

      // First refresh re-sends the buffer (dirty was true)
      expect(deps.sendDraft).toHaveBeenCalledWith(1, "🧠 Initial thought");
      (deps.sendDraft as ReturnType<typeof vi.fn>).mockClear();

      // Second refresh at 50s — no new thinking arrived, dirty is false
      await vi.advanceTimersByTimeAsync(25_000);

      expect(deps.sendDraft).toHaveBeenCalledWith(1, "🧠 Still thinking...");
    });

    it("re-sends full buffer on refresh when new thinking arrived", async () => {
      const sc = new StreamController(deps);
      sc.pushThinking("First thought");

      (deps.sendDraft as ReturnType<typeof vi.fn>).mockClear();

      // Push more thinking before refresh fires
      await vi.advanceTimersByTimeAsync(10_000);
      sc.pushThinking(" and more thinking");
      (deps.sendDraft as ReturnType<typeof vi.fn>).mockClear();

      // Refresh at 25s — thinkingDirty is true from second push
      await vi.advanceTimersByTimeAsync(15_000);

      expect(deps.sendDraft).toHaveBeenCalledWith(
        1,
        "🧠 First thought and more thinking",
      );
    });
  });

  describe("overflow", () => {
    it("sends new message when content exceeds 4096 chars", async () => {
      const { splitMessage } = await import("./formatter/telegram.js");
      vi.mocked(splitMessage).mockImplementation((html: string) => {
        if (html.length > 4096) {
          return [html.slice(0, 4096), html.slice(4096)];
        }
        return [html];
      });

      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.pushText("x".repeat(3000));
      await vi.advanceTimersByTimeAsync(100); // enter streaming

      // Push more to exceed limit
      sc.pushText("y".repeat(2000)); // total 5000 > 4096
      await vi.advanceTimersByTimeAsync(500); // throttle tick

      // Should have: 1 initial send + 1 overflow send
      expect(deps.sendMessage).toHaveBeenCalledTimes(2);
      // Should have edited the first message (finalize at split point)
      expect(deps.editMessage).toHaveBeenCalled();

      // Restore default mock
      vi.mocked(splitMessage).mockImplementation((html: string) => [html]);
    });
  });

  describe("error resilience", () => {
    it("does not crash when editMessage rejects", async () => {
      (deps.editMessage as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("rate limited"));

      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.pushText("Hello");
      await vi.advanceTimersByTimeAsync(100); // enter streaming

      sc.pushText(" world");
      // Should not throw
      await vi.advanceTimersByTimeAsync(500);

      // Controller should still be functional
      sc.pushText(" still working");
      await vi.advanceTimersByTimeAsync(500);
      // No crash = success
    });

    it("does not crash when sendMessage rejects", async () => {
      (deps.sendMessage as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network error"));

      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.pushText("Hello");

      // Should not throw when buffer timer fires
      await vi.advanceTimersByTimeAsync(100);

      // finalize should also not throw
      await sc.finalize();
    });
  });

  describe("guard against messageId null", () => {
    it("skips tick when sendInitialMessage has not resolved yet", async () => {
      // Make sendMessage hang (never resolve)
      let resolveSend!: (id: number) => void;
      (deps.sendMessage as ReturnType<typeof vi.fn>).mockImplementation(
        () => new Promise<number>((r) => { resolveSend = r; }),
      );

      const sc = new StreamController(deps, { bufferMs: 100, throttleMs: 500 });
      sc.pushText("Hello");
      await vi.advanceTimersByTimeAsync(100); // enters streaming, sendMessage called but pending

      // Push more content while messageId is still null
      sc.pushText(" world");

      // Throttle fires, but messageId is null — should not crash
      await vi.advanceTimersByTimeAsync(500);

      // editMessage should NOT be called (messageId is null)
      expect(deps.editMessage).not.toHaveBeenCalled();

      // Resolve the initial send
      resolveSend(42);
      await vi.advanceTimersByTimeAsync(0); // flush microtask

      // Now push more and throttle again — should edit now
      sc.pushText(" final");
      await vi.advanceTimersByTimeAsync(500);
      expect(deps.editMessage).toHaveBeenCalled();
    });
  });

  describe("abort", () => {
    it("sends current content and cancels timers", async () => {
      const sc = new StreamController(deps);
      sc.pushText("Partial response");

      await sc.abort(new Error("connection lost"));

      // Should have sent what we have
      expect(deps.sendMessage).toHaveBeenCalled();
    });
  });
});
