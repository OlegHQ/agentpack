import type { AnthropicRequest } from "../../anthropic/schema.ts";
import { wantsDownstreamStream } from "../../anthropic/stream.ts";
import type { Provider, RequestContext } from "../types.ts";
import { jsonError, jsonResponse, sseResponse } from "../../anthropic/response.ts";
import {
  ALLOWED_MODELS,
  assertAllowedModel,
  FAST_MODEL_ALIASES,
  ModelNotAllowedError,
  resolveModelRequest,
} from "./translate/model-allowlist.ts";
import { InvalidServiceTierError, translateRequest } from "./translate/request.ts";
import { translateStream } from "./translate/stream.ts";
import { accumulateResponse, UpstreamStreamError } from "./translate/accumulate.ts";
import { mapUsageToAnthropic } from "./translate/reducer.ts";
import { CodexError, postCodex } from "./client.ts";
import { countTokens, countTranslatedTokens } from "./count-tokens.ts";
import { summarizeCodexRequestSize, type CodexRequestSizeSummary } from "./request-summary.ts";
import { codexPreviousResponseId, logVerbose } from "../../config.ts";
import { clearContinuation, continuationCandidate, recordContinuation } from "./continuation.ts";
import { codexCli } from "./cli.ts";
import {
  mapUpstreamHttpErrorToResponse,
  mapUpstreamStreamErrorToResponse,
} from "../shared/upstream-errors.ts";

interface SessionCountSnapshot {
  reqId: string;
  model: string;
  messageCount: number;
  toolCount: number;
  tokens: number;
}

interface SessionMessageSnapshot {
  reqId: string;
  model: string;
  messageCount: number;
  toolCount: number;
  localInputTokens?: number;
  translatedInputTokens?: number;
}

interface SessionTimelineState {
  lastCount?: SessionCountSnapshot;
  lastMessage?: SessionMessageSnapshot;
}

const sessionTimeline = new Map<string, SessionTimelineState>();

function sessionState(sessionId?: string): SessionTimelineState | undefined {
  if (!sessionId) return undefined;
  let state = sessionTimeline.get(sessionId);
  if (!state) {
    state = {};
    sessionTimeline.set(sessionId, state);
  }
  return state;
}

function readToolSchemaSummary(
  tools: { type?: string; name?: string; parameters?: unknown; strict?: boolean }[] | undefined,
) {
  const read = tools?.find((tool) => tool.name === "Read");
  if (!read) return undefined;
  const parameters = read.parameters;
  const properties =
    parameters && typeof parameters === "object" && !Array.isArray(parameters)
      ? (parameters as { properties?: unknown }).properties
      : undefined;
  return {
    strict: read.strict,
    schema: summarizeSchema(parameters),
    properties: summarizeSchema(properties),
  };
}

function summarizeSchema(value: unknown): Record<string, unknown> {
  const json = JSON.stringify(value ?? null);
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { type: typeof value, jsonLength: json.length, preview: json.slice(0, 500) };
  }
  const record = value as Record<string, unknown>;
  return {
    jsonLength: json.length,
    keys: Object.keys(record),
    type: record.type,
    required: record.required,
    additionalProperties: record.additionalProperties,
    preview: json.slice(0, 500),
  };
}

function usageWindowTokens(usage: {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
}): number {
  return (
    usage.input_tokens +
    usage.output_tokens +
    usage.cache_creation_input_tokens +
    usage.cache_read_input_tokens
  );
}

function upstreamHeaderSnapshot(headers: Headers): {
  serverModel?: string;
  serverReasoningIncluded: boolean;
} {
  return {
    serverModel: headers.get("OpenAI-Model") || undefined,
    serverReasoningIncluded: headers.has("X-Reasoning-Included"),
  };
}

function warnForHeavyImages(
  log: ReturnType<RequestContext["childLogger"]>,
  requestSize: CodexRequestSizeSummary,
): void {
  if (requestSize.inputImageDataUrlBytes < 1_000_000) return;
  log.warn("large inline images in codex request", {
    inputImagePartCount: requestSize.inputImagePartCount,
    inputImageDataUrlBytes: requestSize.inputImageDataUrlBytes,
    bodyJsonBytes: requestSize.bodyJsonBytes,
    inputJsonBytes: requestSize.inputJsonBytes,
    largestInputImages: requestSize.largestInputImages,
  });
}

function invalidServiceTierResponse(err: InvalidServiceTierError): Response {
  return jsonError(400, "invalid_request_error", err.message);
}

type PrepareCodexRequestResult =
  | {
      ok: true;
      translated: ReturnType<typeof translateRequest>;
      resolvedModel: string;
      resolvedServiceTier: ReturnType<typeof resolveModelRequest>["serviceTier"];
    }
  | { ok: false; response: Response };

function prepareCodexRequest(
  body: AnthropicRequest,
  options: { sessionId?: string } = {},
): PrepareCodexRequestResult {
  const resolved = resolveModelRequest(body.model);
  try {
    assertAllowedModel(resolved.model);
    const translated = translateRequest(
      { ...body, model: resolved.model },
      { sessionId: options.sessionId, serviceTier: resolved.serviceTier },
    );
    return {
      ok: true,
      translated,
      resolvedModel: resolved.model,
      resolvedServiceTier: resolved.serviceTier,
    };
  } catch (err) {
    if (err instanceof ModelNotAllowedError) {
      return {
        ok: false,
        response: jsonError(
          400,
          "invalid_request_error",
          `Model "${body.model}" resolves to unsupported model "${err.model}"`,
        ),
      };
    }
    if (err instanceof InvalidServiceTierError) {
      return {
        ok: false,
        response: invalidServiceTierResponse(err),
      };
    }
    throw err;
  }
}

async function handleCountTokens(body: AnthropicRequest, ctx: RequestContext): Promise<Response> {
  const log = ctx.childLogger("provider.codex");
  const prepared = prepareCodexRequest(body);
  if (!prepared.ok) return prepared.response;
  const translated = prepared.translated;
  const resolvedModel = prepared.resolvedModel;
  const tokens = countTranslatedTokens(translated);
  const messageCount = body.messages?.length ?? 0;
  const toolCount = body.tools?.length ?? 0;
  const state = sessionState(ctx.sessionId);
  log.debug("count_tokens", { tokens });
  if (state) {
    state.lastCount = {
      reqId: ctx.reqId,
      model: body.model,
      messageCount,
      toolCount,
      tokens,
    };
  }
  if (logVerbose()) {
    log.info("compaction telemetry", {
      phase: "count_tokens",
      model: body.model,
      resolvedModel,
      tokens,
      messageCount,
      toolCount,
      previousMessageReqId: state?.lastMessage?.reqId,
      previousMessageModel: state?.lastMessage?.model,
      previousMessageCount: state?.lastMessage?.messageCount,
      previousMessageToolCount: state?.lastMessage?.toolCount,
      previousMessageLocalInputTokens: state?.lastMessage?.localInputTokens,
      previousMessageTranslatedInputTokens: state?.lastMessage?.translatedInputTokens,
    });
  }
  return jsonResponse({ input_tokens: tokens });
}

async function handleMessages(body: AnthropicRequest, ctx: RequestContext): Promise<Response> {
  const log = ctx.childLogger("provider.codex");
  const messageId = `msg_${crypto.randomUUID().replace(/-/g, "")}`;
  const wantStream = wantsDownstreamStream(body);
  const messageCount = body.messages?.length ?? 0;
  const toolCount = body.tools?.length ?? 0;
  const contextManagement = body.context_management;
  const state = sessionState(ctx.sessionId);

  log.debug("anthropic request", {
    model: body.model,
    messageCount,
    toolCount,
    stream: wantStream,
    requestedMaxTokens: body.max_tokens,
    hasContextManagement: contextManagement !== undefined,
    hasJsonSchemaFormat: body.output_config?.format?.type === "json_schema",
  });
  if (logVerbose()) log.debug("anthropic request body", { body });

  const prepared = prepareCodexRequest(body, { sessionId: ctx.sessionId });
  if (!prepared.ok) return prepared.response;
  const translated = prepared.translated;
  const resolvedModel = prepared.resolvedModel;
  const requestSize = summarizeCodexRequestSize(translated);
  warnForHeavyImages(log, requestSize);
  const localInputTokens = logVerbose() ? countTokens(body) : undefined;
  const translatedInputTokens = logVerbose() ? countTranslatedTokens(translated) : undefined;
  if (state) {
    state.lastMessage = {
      reqId: ctx.reqId,
      model: body.model,
      messageCount,
      toolCount,
      localInputTokens,
      translatedInputTokens,
    };
  }
  log.debug("translated request", {
    requestedModel: body.model,
    resolvedModel,
    inputItems: translated.input.length,
    tools: translated.tools?.length ?? 0,
    readToolSchema: readToolSchemaSummary(translated.tools),
    hasInstructions: !!translated.instructions,
    requestedMaxTokens: body.max_tokens,
    hasContextManagement: contextManagement !== undefined,
    promptCacheKey: translated.prompt_cache_key,
    requestSize,
  });
  if (logVerbose()) log.debug("translated request body", { body: translated });
  if (logVerbose()) {
    log.info("compaction telemetry", {
      phase: "translated_request",
      requestedModel: body.model,
      resolvedModel,
      messageCount,
      toolCount,
      localInputTokens,
      translatedInputTokens,
      inputItems: translated.input.length,
      translatedToolCount: translated.tools?.length ?? 0,
      readToolSchema: readToolSchemaSummary(translated.tools),
      hasInstructions: !!translated.instructions,
      requestedMaxTokens: body.max_tokens,
      hasContextManagement: contextManagement !== undefined,
      contextManagement,
      requestSize,
      previousCountReqId: state?.lastCount?.reqId,
      previousCountModel: state?.lastCount?.model,
      previousCountTokens: state?.lastCount?.tokens,
      previousCountMessageCount: state?.lastCount?.messageCount,
      previousCountToolCount: state?.lastCount?.toolCount,
    });
  }

  const previousResponseIdEnabled = codexPreviousResponseId();
  const continuation = continuationCandidate(ctx.sessionId, translated, previousResponseIdEnabled);
  log.debug("codex continuation", {
    enabled: previousResponseIdEnabled,
    previousResponseId: continuation.previousResponseId,
    inputDeltaCount: continuation.inputDeltaCount,
    disabledReason: continuation.disabledReason,
  });

  let upstream;
  try {
    upstream = await postCodex(translated, ctx, { continuation });
  } catch (err) {
    clearContinuation(ctx.sessionId);
    if (err instanceof CodexError) {
      log.warn("codex error", { status: err.status, detail: err.detail });
      return mapUpstreamHttpErrorToResponse(err);
    }
    throw err;
  }

  if (wantStream) {
    const { serverModel, serverReasoningIncluded } = upstreamHeaderSnapshot(upstream.headers);
    const stream = translateStream(upstream.body, {
      messageId,
      model: body.model,
      log: ctx.childLogger("codex.stream"),
      reqId: ctx.reqId,
      signal: ctx.signal,
      upstreamHeaders: upstream.headers,
      traffic: ctx.traffic,
      requestSize,
      onFinish: (finish) => {
        if (finish.continuationEligible) {
          recordContinuation(ctx.sessionId, translated, finish.responseId, finish.outputItems);
        } else {
          clearContinuation(ctx.sessionId);
        }
        if (logVerbose()) {
          const mappedUsage = finish.usage ? mapUsageToAnthropic(finish.usage) : undefined;
          log.info("compaction telemetry", {
            phase: "upstream_finish",
            mode: "stream",
            requestedModel: body.model,
            resolvedModel,
            serverModel,
            serverReasoningIncluded,
            messageCount,
            toolCount,
            localInputTokens,
            translatedInputTokens,
            requestedMaxTokens: body.max_tokens,
            hasContextManagement: contextManagement !== undefined,
            contextManagement,
            upstreamInputTokens: finish.usage?.input_tokens ?? 0,
            upstreamOutputTokens: finish.usage?.output_tokens ?? 0,
            upstreamCachedInputTokens: finish.usage?.input_tokens_details?.cached_tokens ?? 0,
            upstreamReasoningTokens: finish.usage?.output_tokens_details?.reasoning_tokens ?? 0,
            mappedInputTokens: mappedUsage?.input_tokens ?? 0,
            mappedOutputTokens: mappedUsage?.output_tokens ?? 0,
            mappedCachedInputTokens: mappedUsage?.cache_read_input_tokens ?? 0,
            mappedContextWindowTokens: mappedUsage ? usageWindowTokens(mappedUsage) : 0,
            stopReason: finish.stopReason,
          });
        }
      },
      onInvalidateContinuation: () => clearContinuation(ctx.sessionId),
    });
    return sseResponse(stream);
  }

  try {
    const result = await accumulateResponse(upstream.body, {
      messageId,
      model: body.model,
      log: ctx.childLogger("codex.accumulate"),
      traffic: ctx.traffic,
    });
    if (result.continuationEligible) {
      recordContinuation(ctx.sessionId, translated, result.responseId, result.outputItems);
    } else {
      clearContinuation(ctx.sessionId);
    }
    if (logVerbose()) {
      const { serverModel, serverReasoningIncluded } = upstreamHeaderSnapshot(upstream.headers);
      log.info("compaction telemetry", {
        phase: "upstream_finish",
        mode: "non_stream",
        requestedModel: body.model,
        resolvedModel,
        serverModel,
        serverReasoningIncluded,
        messageCount,
        toolCount,
        localInputTokens,
        translatedInputTokens,
        requestedMaxTokens: body.max_tokens,
        hasContextManagement: contextManagement !== undefined,
        contextManagement,
        upstreamInputTokens: result.rawUsage?.input_tokens ?? 0,
        upstreamOutputTokens: result.rawUsage?.output_tokens ?? 0,
        upstreamCachedInputTokens: result.rawUsage?.input_tokens_details?.cached_tokens ?? 0,
        upstreamReasoningTokens: result.rawUsage?.output_tokens_details?.reasoning_tokens ?? 0,
        mappedInputTokens: result.response.usage.input_tokens,
        mappedOutputTokens: result.response.usage.output_tokens,
        mappedCachedInputTokens: result.response.usage.cache_read_input_tokens,
        mappedContextWindowTokens: usageWindowTokens(result.response.usage),
        stopReason: result.response.stop_reason,
      });
    }
    return jsonResponse(result.response);
  } catch (err) {
    clearContinuation(ctx.sessionId);
    if (err instanceof UpstreamStreamError) {
      log.warn("upstream stream error (non-streaming)", {
        kind: err.kind,
        message: err.message,
      });
      return mapUpstreamStreamErrorToResponse(err);
    }
    throw err;
  }
}

export const codexProvider: Provider = {
  name: "codex",
  supportedModels: new Set([...ALLOWED_MODELS, ...FAST_MODEL_ALIASES]),
  handleMessages,
  handleCountTokens,
  cli: codexCli,
};
