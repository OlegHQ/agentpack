import { anthropicErrorBody, jsonError, jsonResponse } from "../../anthropic/response.ts";

export interface UpstreamHttpError {
  status: number;
  detail?: string;
  message: string;
  meta?: {
    retryAfter?: string;
  };
}

export interface UpstreamStreamErrorLike {
  kind: string;
  retryAfterSeconds?: number;
  message: string;
}

export function mapUpstreamHttpErrorToResponse(err: UpstreamHttpError): Response {
  if (err.status === 429) {
    const headers: Record<string, string> = {};
    if (err.meta?.retryAfter) headers["retry-after"] = err.meta.retryAfter;
    return jsonResponse(anthropicErrorBody("rate_limit_error", err.detail || err.message), {
      status: 429,
      headers,
    });
  }

  const type = err.status === 401 || err.status === 403 ? "authentication_error" : "api_error";
  return jsonError(err.status, type, err.detail || err.message);
}

export function mapUpstreamStreamErrorToResponse(err: UpstreamStreamErrorLike): Response {
  if (err.kind === "rate_limit") {
    const headers: Record<string, string> = {};
    if (err.retryAfterSeconds) headers["retry-after"] = String(err.retryAfterSeconds);
    return jsonResponse(anthropicErrorBody("rate_limit_error", err.message), {
      status: 429,
      headers,
    });
  }
  return jsonError(502, "api_error", err.message);
}
