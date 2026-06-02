import { describe, it, expect } from "vitest";
import { formatMarkdown, closeOpenTags, splitMessage } from "./telegram.js";

// ---------------------------------------------------------------------------
// formatMarkdown
// ---------------------------------------------------------------------------

describe("formatMarkdown", () => {
  it("escapes HTML special characters in plain text", () => {
    expect(formatMarkdown("a & b < c > d")).toBe("a &amp; b &lt; c &gt; d");
  });

  it("converts fenced code block with language", () => {
    const result = formatMarkdown("```js\nconsole.log('hi')\n```");
    expect(result).toBe(
      `<blockquote><pre><code class="language-js">console.log('hi')\n</code></pre></blockquote>`,
    );
  });

  it("converts fenced code block without language", () => {
    const result = formatMarkdown("```\nhello\n```");
    expect(result).toBe("<blockquote><pre>hello\n</pre></blockquote>");
  });

  it("escapes HTML inside code blocks", () => {
    const result = formatMarkdown("```\n<b>bold</b>\n```");
    expect(result).toContain("&lt;b&gt;bold&lt;/b&gt;");
  });

  it("handles unclosed fenced code block (streaming safety)", () => {
    const result = formatMarkdown("```js\nconsole.log('hi')");
    expect(result).toContain(`<code class="language-js">`);
    expect(result).toContain("console.log('hi')");
  });

  it("converts inline code", () => {
    expect(formatMarkdown("use `foo()` here")).toBe("use <code>foo()</code> here");
  });

  it("escapes HTML inside inline code", () => {
    expect(formatMarkdown("`a < b`")).toBe("<code>a &lt; b</code>");
  });

  it("converts bold", () => {
    expect(formatMarkdown("**hello**")).toBe("<b>hello</b>");
  });

  it("converts italic with underscores", () => {
    expect(formatMarkdown("_hello_")).toBe("<i>hello</i>");
  });

  it("converts italic with stars", () => {
    expect(formatMarkdown("*hello*")).toBe("<i>hello</i>");
  });

  it("converts strikethrough", () => {
    expect(formatMarkdown("~~hello~~")).toBe("<s>hello</s>");
  });

  it("converts links", () => {
    expect(formatMarkdown("[click](https://example.com)")).toBe(
      '<a href="https://example.com">click</a>',
    );
  });

  it("converts headers to bold", () => {
    expect(formatMarkdown("## Hello World")).toBe("<b>Hello World</b>");
  });

  it("converts blockquotes", () => {
    const result = formatMarkdown("> some quote\n");
    expect(result).toContain("<blockquote>");
    expect(result).toContain("some quote");
  });

  it("does not double-escape placeholders", () => {
    // Code block content should not be processed by markdown transforms
    const result = formatMarkdown("```\n**not bold**\n```");
    expect(result).toContain("**not bold**");
    expect(result).not.toContain("<b>");
  });
});

// ---------------------------------------------------------------------------
// closeOpenTags
// ---------------------------------------------------------------------------

describe("closeOpenTags", () => {
  it("returns unchanged string when all tags are closed", () => {
    expect(closeOpenTags("<b>hello</b>")).toBe("<b>hello</b>");
  });

  it("closes a single unclosed tag", () => {
    expect(closeOpenTags("<b>hello")).toBe("<b>hello</b>");
  });

  it("closes multiple unclosed tags in reverse order", () => {
    expect(closeOpenTags("<b><i>hello")).toBe("<b><i>hello</i></b>");
  });

  it("handles already-closed inner tag with unclosed outer", () => {
    expect(closeOpenTags("<b><i>hello</i>")).toBe("<b><i>hello</i></b>");
  });

  it("returns empty string unchanged", () => {
    expect(closeOpenTags("")).toBe("");
  });

  it("ignores non-tracked tags", () => {
    expect(closeOpenTags("<div>hello")).toBe("<div>hello");
  });

  it("handles unclosed code tag", () => {
    expect(closeOpenTags("<code>snippet")).toBe("<code>snippet</code>");
  });
});

// ---------------------------------------------------------------------------
// splitMessage
// ---------------------------------------------------------------------------

describe("splitMessage", () => {
  it("returns single-element array for short messages", () => {
    const result = splitMessage("hello world");
    expect(result).toEqual(["hello world"]);
  });

  it("splits at paragraph boundary", () => {
    const para1 = "a".repeat(3000);
    const para2 = "b".repeat(3000);
    const text = `${para1}\n\n${para2}`;
    const chunks = splitMessage(text);
    expect(chunks.length).toBeGreaterThan(1);
    expect(chunks[0]).toContain(para1);
  });

  it("splits at newline boundary when no paragraph break fits", () => {
    const line1 = "a".repeat(2500);
    const line2 = "b".repeat(2500);
    const text = `${line1}\n${line2}`;
    const chunks = splitMessage(text);
    expect(chunks.length).toBeGreaterThan(1);
    expect(chunks[0]).toContain(line1);
  });

  it("heals open HTML tags across chunk boundaries", () => {
    // Build a message that forces a split inside a <b> tag span
    const prefix = "<b>" + "x".repeat(4000);
    const suffix = "y".repeat(100) + "</b>";
    const text = prefix + suffix;
    const chunks = splitMessage(text);
    expect(chunks.length).toBeGreaterThan(1);
    // First chunk should be closed
    expect(chunks[0]).toMatch(/<\/b>$/);
    // Last chunk should reopen the tag
    expect(chunks[chunks.length - 1]).toMatch(/<\/b>$/);
  });

  it("each chunk fits within maxLen", () => {
    const text = "word ".repeat(2000);
    const maxLen = 500;
    const chunks = splitMessage(text, maxLen);
    for (const chunk of chunks) {
      expect(chunk.length).toBeLessThanOrEqual(maxLen);
    }
  });

  it("reassembled chunks equal original text (no HTML)", () => {
    const text = "hello world ".repeat(500);
    const chunks = splitMessage(text);
    expect(chunks.join("")).toBe(text);
  });
});
