import type { Provider, RequestContext } from "../types.ts";
import type { AnthropicRequest } from "../../anthropic/schema.ts";
import { wantsDownstreamStream } from "../../anthropic/stream.ts";
import { jsonError, jsonResponse, sseResponse } from "../../anthropic/response.ts";
import {
  assertAllowedModel,
  ModelNotAllowedError,
  resolveModel,
} from "./translate/model-allowlist.ts";
import { translateRequest } from "./translate/request.ts";
import { translateStream } from "./translate/stream.ts";
import { accumulateResponse, UpstreamStreamError } from "./translate/accumulate.ts";
import { mapUsageToAnthropic } from "./translate/reducer.ts";
import { countTokens, countTranslatedTokens } from "./count-tokens.ts";
import { KimiError, postKimi } from "./client.ts";
import { logVerbose } from "../../config.ts";
import { kimiCli } from "./cli.ts";
import { mapUpstreamHttpErrorToResponse, mapUpstreamStreamErrorToResponse } from "../shared/upstream-errors.ts";

async function handleCountTokens(body: AnthropicRequest, ctx: RequestContext): Promise<Response> {
  const log = ctx.childLogger("provider.kimi");
  const resolvedModel = resolveModel(body.model);
  const translated = translateRequest({ ...body, model: resolvedModel });
  const tokens = countTranslatedTokens(translated);
  log.debug("count_tokens", { tokens });
  return jsonResponse({ input_tokens: tokens });
}

async function handleMessages(body: AnthropicRequest, ctx: RequestContext): Promise<Response> {
  const log = ctx.childLogger("provider.kimi");
  const messageId = `msg_${crypto.randomUUID().replace(/-/g, "")}`;
  const wantStream = wantsDownstreamStream(body);
  const messageCount = body.messages?.length ?? 0;
  const toolCount = body.tools?.length ?? 0;

  log.debug("anthropic request", {
    model: body.model,
    messageCount,
    toolCount,
    stream: wantStream,
    requestedMaxTokens: body.max_tokens,
  });
  if (logVerbose()) log.debug("anthropic request body", { body });

  const resolvedModel = resolveModel(body.model);
  try {
    assertAllowedModel(resolvedModel);
  } catch (err) {
    if (err instanceof ModelNotAllowedError) {
      return jsonError(
        400,
        "invalid_request_error",
        `Model "${body.model}" resolves to unsupported model "${err.model}"`,
      );
    }
    throw err;
  }

  const translated = translateRequest(
    { ...body, model: resolvedModel },
    { sessionId: ctx.sessionId },
  );
  const localInputTokens = countTokens(body);
  const translatedInputTokens = countTranslatedTokens(translated);
  log.debug("translated request", {
    requestedModel: body.model,
    resolvedModel,
    messageCount: translated.messages.length,
    toolCount: translated.tools?.length ?? 0,
    localInputTokens,
    translatedInputTokens,
    promptCacheKey: translated.prompt_cache_key,
    reasoningEffort: translated.reasoning_effort,
    thinking: translated.thinking?.type,
    maxTokens: translated.max_tokens,
  });
  if (logVerbose()) log.debug("translated request body", { body: translated });

  let upstream;
  try {
    upstream = await postKimi(translated, ctx);
  } catch (err) {
    if (err instanceof KimiError) {
      log.warn("kimi error", { status: err.status, detail: err.detail });
      return mapUpstreamHttpErrorToResponse(err);
    }
    throw err;
  }

  if (wantStream) {
    const stream = translateStream(upstream.body, {
      messageId,
      model: body.model,
      log: ctx.childLogger("kimi.stream"),
      requestStartTime: upstream.requestStartTime,
      traffic: ctx.traffic,
      onFinish: (finish) => {
        const mappedUsage = finish.usage ? mapUsageToAnthropic(finish.usage) : undefined;
        log.debug("stream finish", {
          stopReason: finish.stopReason,
          upstreamInputTokens: finish.usage?.prompt_tokens ?? 0,
          upstreamOutputTokens: finish.usage?.completion_tokens ?? 0,
          upstreamCachedInputTokens:
            finish.usage?.prompt_tokens_details?.cached_tokens ?? finish.usage?.cached_tokens ?? 0,
          upstreamReasoningTokens: finish.usage?.completion_tokens_details?.reasoning_tokens ?? 0,
          mappedInputTokens: mappedUsage?.input_tokens ?? 0,
          mappedOutputTokens: mappedUsage?.output_tokens ?? 0,
          mappedCacheReadTokens: mappedUsage?.cache_read_input_tokens ?? 0,
        });
      },
    });
    return sseResponse(stream);
  }

  try {
    const result = await accumulateResponse(upstream.body, {
      messageId,
      model: body.model,
      log: ctx.childLogger("kimi.accumulate"),
      traffic: ctx.traffic,
    });
    return jsonResponse(result.response);
  } catch (err) {
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

export const kimiProvider: Provider = {
  name: "kimi",
  supportedModels: new Set(["kimi-for-coding", "kimi-k2.6", "k2.6"]),
  handleMessages,
  handleCountTokens,
  cli: kimiCli,
};
