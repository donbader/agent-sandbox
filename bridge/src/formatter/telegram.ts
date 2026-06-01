// --- Regex patterns for markdown → HTML ---

const FENCED_CODE_RE = /```(\w*)\n?([\s\S]*?)```/g;
const UNCLOSED_FENCED_CODE_RE = /```(\w*)\n?([\s\S]*)$/;
const INLINE_CODE_RE = /`([^`\n]+)`/g;
const UNPAIRED_BACKTICK_RE = /`([^`\n]+)/gm;
const LONE_BACKTICK_RE = /`\s*$/gm;
const BLOCKQUOTE_RE = /(?:^>[ ]?(.*?)$\n?)+/gm;
const HEADER_RE = /^#{1,6}\s+(.+)$/gm;
const LINK_RE = /\[([^\]]+)\]\(([^)]+)\)/g;
const BOLD_RE = /\*\*(.+?)\*\*/g;
const STRIKE_RE = /~~(.+?)~~/g;
const ITALIC_UNDERSCORE_RE = /\b_([^_]+?)_\b/g;
const ITALIC_STAR_RE = /\*([^*]+?)\*/g;

const TRACKED_TAGS = ["b", "i", "s", "u", "code", "pre", "blockquote", "a"];

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export const MAX_MESSAGE_LENGTH = 4096;

/**
 * Convert markdown text to Telegram-compatible HTML.
 * Handles fenced code blocks, inline code, bold, italic, links, etc.
 */
export function formatMarkdown(text: string): string {
  const codeBlocks: string[] = [];
  const inlineCodes: string[] = [];
  const blockquotes: string[] = [];

  // Step 1: Extract fenced code blocks → placeholder
  let result = text.replace(FENCED_CODE_RE, (_match, lang, code) => {
    const escaped = escapeHtml(code);
    const block = lang
      ? `<blockquote><pre><code class="language-${lang}">${escaped}</code></pre></blockquote>`
      : `<blockquote><pre>${escaped}</pre></blockquote>`;
    const idx = codeBlocks.length;
    codeBlocks.push(block);
    return `\x00CODEBLOCK_${idx}\x00`;
  });

  // Step 1b: Extract unclosed fenced code block (truncated streaming output)
  result = result.replace(UNCLOSED_FENCED_CODE_RE, (_match, lang, code) => {
    const escaped = escapeHtml(code);
    const block = lang
      ? `<blockquote><pre><code class="language-${lang}">${escaped}</code></pre></blockquote>`
      : `<blockquote><pre>${escaped}</pre></blockquote>`;
    const idx = codeBlocks.length;
    codeBlocks.push(block);
    return `\x00CODEBLOCK_${idx}\x00`;
  });

  // Step 2: Extract inline code → placeholder
  result = result.replace(INLINE_CODE_RE, (_match, code) => {
    const escaped = escapeHtml(code);
    const idx = inlineCodes.length;
    inlineCodes.push(`<code>${escaped}</code>`);
    return `\x00INLINE_${idx}\x00`;
  });

  // Step 2b: Extract unpaired backticks
  result = result.replace(UNPAIRED_BACKTICK_RE, (_match, code) => {
    const escaped = escapeHtml(code);
    const idx = inlineCodes.length;
    inlineCodes.push(`<code>${escaped}</code>`);
    return `\x00INLINE_${idx}\x00`;
  });

  // Step 2c: Strip remaining lone backticks
  result = result.replace(LONE_BACKTICK_RE, "");

  // Step 3: Extract blockquotes → placeholder
  result = result.replace(BLOCKQUOTE_RE, (match) => {
    const lines = match
      .split("\n")
      .filter((l) => l.length > 0)
      .map((l) => l.replace(/^>\s?/, ""));
    const content = escapeHtml(lines.join("\n"));
    const idx = blockquotes.length;
    blockquotes.push(`<blockquote>${content}</blockquote>`);
    return `\x00BLOCKQUOTE_${idx}\x00`;
  });

  // Step 4: Escape remaining text
  result = escapeHtml(result);

  // Step 5: Apply markdown transforms on escaped text
  result = result.replace(HEADER_RE, "<b>$1</b>");
  result = result.replace(LINK_RE, '<a href="$2">$1</a>');
  result = result.replace(BOLD_RE, "<b>$1</b>");
  result = result.replace(STRIKE_RE, "<s>$1</s>");
  result = result.replace(ITALIC_UNDERSCORE_RE, "<i>$1</i>");
  result = result.replace(ITALIC_STAR_RE, "<i>$1</i>");

  // Step 5b: Handle unpaired bold/strike
  result = result.replace(/\*\*([^\n]+)/gm, "<b>$1</b>");
  result = result.replace(/~~([^\n]+)/gm, "<s>$1</s>");
  result = result.replace(/\*\*\s*$/gm, "");
  result = result.replace(/~~\s*$/gm, "");

  // Step 6: Restore placeholders
  for (let i = 0; i < blockquotes.length; i++) {
    result = result.replace(`\x00BLOCKQUOTE_${i}\x00`, blockquotes[i]);
  }
  for (let i = 0; i < inlineCodes.length; i++) {
    result = result.replace(`\x00INLINE_${i}\x00`, inlineCodes[i]);
  }
  for (let i = 0; i < codeBlocks.length; i++) {
    result = result.replace(`\x00CODEBLOCK_${i}\x00`, codeBlocks[i]);
  }

  return result;
}

/**
 * Close any unclosed HTML tags (for streaming safety).
 * When streaming partial content, tags may be left open.
 */
export function closeOpenTags(html: string): string {
  const stack: string[] = [];
  let i = 0;

  while (i < html.length) {
    if (html[i] === "<") {
      const closeIdx = html.indexOf(">", i);
      if (closeIdx === -1) break;
      const tagContent = html.slice(i + 1, closeIdx);
      if (tagContent.startsWith("/")) {
        const tagName = tagContent.slice(1).split(/\s/)[0];
        const pos = stack.lastIndexOf(tagName);
        if (pos !== -1) stack.splice(pos, 1);
      } else if (!tagContent.startsWith("!")) {
        const tagName = tagContent.split(/\s/)[0];
        if (tagName && !tagContent.endsWith("/") && TRACKED_TAGS.includes(tagName)) {
          stack.push(tagName);
        }
      }
      i = closeIdx + 1;
    } else {
      i++;
    }
  }

  let result = html;
  for (let j = stack.length - 1; j >= 0; j--) {
    result += `</${stack[j]}>`;
  }
  return result;
}

/**
 * Split a message into chunks that fit within Telegram's limit.
 * Heals HTML tags across chunk boundaries.
 */
export function splitMessage(text: string, maxLen = MAX_MESSAGE_LENGTH): string[] {
  if (text.length <= maxLen) return [text];

  const chunks = splitRaw(text, maxLen);
  healHtmlAcrossChunks(chunks, maxLen);
  return chunks;
}

function splitRaw(text: string, maxLen: number): string[] {
  const chunks: string[] = [];
  let remaining = text;

  while (remaining.length > 0) {
    if (remaining.length <= maxLen) {
      chunks.push(remaining);
      break;
    }

    let boundary = maxLen;
    const candidate = remaining.slice(0, boundary);

    const paraIdx = candidate.lastIndexOf("\n\n");
    if (paraIdx > 0) {
      boundary = paraIdx + 2;
    } else {
      const nlIdx = candidate.lastIndexOf("\n");
      if (nlIdx > 0) {
        boundary = nlIdx + 1;
      } else {
        const sentIdx = candidate.lastIndexOf(". ");
        if (sentIdx > 0) {
          boundary = sentIdx + 2;
        }
      }
    }

    // Never split inside an HTML tag
    const chunk = remaining.slice(0, boundary);
    const lastOpen = chunk.lastIndexOf("<");
    if (lastOpen > 0 && !chunk.slice(lastOpen).includes(">")) {
      boundary = lastOpen;
    }

    chunks.push(remaining.slice(0, boundary));
    remaining = remaining.slice(boundary);
  }

  return chunks;
}

interface OpenTag {
  name: string;
  full: string;
}

function healHtmlAcrossChunks(chunks: string[], maxLen: number): void {
  let carryOpen: OpenTag[] = [];
  let i = 0;

  while (i < chunks.length) {
    if (carryOpen.length > 0) {
      chunks[i] = carryOpen.map((t) => t.full).join("") + chunks[i];
    }

    const stack = parseOpenTags(chunks[i]);
    const suffix = buildClosingSuffix(stack);

    if (chunks[i].length + suffix.length > maxLen) {
      let truncateAt = maxLen - suffix.length;
      if (truncateAt < 0) truncateAt = 0;

      const lastOpen = chunks[i].lastIndexOf("<", truncateAt);
      if (lastOpen >= 0 && !chunks[i].slice(lastOpen, truncateAt + 1).includes(">")) {
        truncateAt = lastOpen;
      }

      const overflow = chunks[i].slice(truncateAt);
      const truncated = chunks[i].slice(0, truncateAt);

      const truncStack = parseOpenTags(truncated);
      chunks[i] = truncated + buildClosingSuffix(truncStack);

      if (overflow.length > 0) {
        chunks.splice(i + 1, 0, overflow);
      }
      carryOpen = truncStack;
    } else {
      chunks[i] = chunks[i] + suffix;
      carryOpen = stack;
    }

    i++;
  }
}

function parseOpenTags(html: string): OpenTag[] {
  const stack: OpenTag[] = [];
  let i = 0;
  while (i < html.length) {
    if (html[i] === "<") {
      const closeIdx = html.indexOf(">", i);
      if (closeIdx === -1) break;
      const tagContent = html.slice(i + 1, closeIdx);
      if (tagContent.startsWith("/")) {
        const tagName = tagContent.slice(1).split(/\s/)[0];
        const pos = findLastIndex(stack, (t) => t.name === tagName);
        if (pos !== -1) stack.splice(pos, 1);
      } else if (!tagContent.startsWith("!")) {
        const tagName = tagContent.split(/\s/)[0];
        if (tagName && !tagContent.endsWith("/")) {
          stack.push({ name: tagName, full: html.slice(i, closeIdx + 1) });
        }
      }
      i = closeIdx + 1;
    } else {
      i++;
    }
  }
  return stack;
}

function buildClosingSuffix(stack: OpenTag[]): string {
  return stack.slice().reverse().map((t) => `</${t.name}>`).join("");
}

function findLastIndex<T>(arr: T[], predicate: (item: T) => boolean): number {
  for (let i = arr.length - 1; i >= 0; i--) {
    if (predicate(arr[i])) return i;
  }
  return -1;
}
