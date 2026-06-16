export interface AnthropicWebSearchResult {
  type: "web_search_result";
  title: string;
  url: string;
  page_age?: string | null;
}

export interface AnthropicServerToolUseContent {
  type: "server_tool_use";
  id: string;
  name: "web_search";
  input: { query: string };
}

export interface AnthropicWebSearchToolResultContent {
  type: "web_search_tool_result";
  tool_use_id: string;
  content: AnthropicWebSearchResult[];
}

export interface WebSearchCompatBlock {
  index: number;
  content: AnthropicServerToolUseContent | AnthropicWebSearchToolResultContent;
}

export function serverToolUseIdFromCodexWebSearchId(id: string | undefined): string {
  const suffix = (id || crypto.randomUUID()).replace(/[^A-Za-z0-9_]/g, "_");
  return `srvtoolu_${suffix}`;
}

export function extractWebSearchResultsFromText(text: string): AnthropicWebSearchResult[] {
  const results = new Map<string, AnthropicWebSearchResult>();
  for (const match of text.matchAll(/\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g)) {
    const title = cleanTitle(match[1] ?? "");
    const url = cleanUrl(match[2] ?? "");
    if (!url || results.has(url)) continue;
    results.set(url, { type: "web_search_result", title: title || fallbackTitle(url), url });
  }

  for (const match of text.matchAll(/\bhttps?:\/\/[^\s<>"')\]]+/g)) {
    const rawUrl = match[0] ?? "";
    const url = cleanUrl(rawUrl);
    if (!url || results.has(url)) continue;
    const title = titleNearUrl(text, match.index ?? 0, url);
    results.set(url, { type: "web_search_result", title: title || fallbackTitle(url), url });
  }

  return Array.from(results.values());
}

export function buildWebSearchCompatBlocks(
  searches: Array<{ index: number; resultIndex: number; id: string; query: string }>,
  text: string,
): WebSearchCompatBlock[] {
  const results = extractWebSearchResultsFromText(text);
  return searches.flatMap((search) => [
    {
      index: search.index,
      content: {
        type: "server_tool_use" as const,
        id: search.id,
        name: "web_search" as const,
        input: { query: search.query },
      },
    },
    {
      index: search.resultIndex,
      content: {
        type: "web_search_tool_result" as const,
        tool_use_id: search.id,
        content: results,
      },
    },
  ]);
}

function cleanUrl(value: string): string {
  let out = value.trim();
  while (/[.,;:!?]$/.test(out)) out = out.slice(0, -1);
  try {
    return new URL(out).toString();
  } catch {
    return "";
  }
}

function titleNearUrl(text: string, urlStart: number, url: string): string {
  const before = text
    .slice(0, urlStart)
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !line.includes("http"))
    .slice(-3)
    .reverse();
  for (const line of before) {
    const title = cleanTitle(line);
    if (title) return title;
  }
  return fallbackTitle(url);
}

function cleanTitle(value: string): string {
  const noListMarker = value
    .replace(/^\s*(?:[-*+]|\d+[.)])\s+/, "")
    .replace(/\*\*/g, "")
    .replace(/`/g, "")
    .trim();
  const [prefix] = noListMarker.split(/\s(?:-|\u2013|\u2014)\s/);
  return (prefix ?? noListMarker).trim();
}

function fallbackTitle(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return url;
  }
}
