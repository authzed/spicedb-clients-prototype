import { Code, ConnectError } from "@connectrpc/connect";
import { DEFAULT_RETRYABLE_CODES, InvalidArgumentError, type SpiceDBError } from "./errors.js";

/** Describes a retry about to happen, for {@link RetryPolicy.onRetry}. */
export interface RetryEvent {
    /** The client method being retried, e.g. `"readSchema"`. */
    readonly operation: string;
    /** The attempt that failed, starting at 1. */
    readonly attempt: number;
    /** That attempt's error, as a native error. */
    readonly error: SpiceDBError;
    /** The backoff before the next attempt, in milliseconds. */
    readonly delayMs: number;
}

/**
 * Retry policy for idempotent reads, including stream establishment. A
 * client-wide policy never replays a mutation; a per-call policy
 * (`CallOptions.retryPolicy`) may opt in only the two mutations that are
 * idempotent on replay: touch-only writes without preconditions and
 * `writeSchema`.
 */
export interface RetryPolicy {
    /** Retries after the first attempt. Zero means a single attempt. */
    readonly maxRetries: number;
    /**
     * Transport codes that are retried. The default is Unavailable and Aborted;
     * a caller may opt into others, such as ResourceExhausted.
     */
    readonly retryableCodes: readonly Code[];
    /** Upper bound of the first full-jitter delay; each retry doubles it. */
    readonly initialBackoffMs: number;
    /** Caps the delay bound. Absent means uncapped. */
    readonly maxBackoffMs?: number;
    /** Called before each backoff, e.g. to log the retry. */
    readonly onRetry?: (event: RetryEvent) => void;
}

/** The built-in policy: three retries of Unavailable and Aborted, full-jitter backoff from 100 ms. */
export const DEFAULT_RETRY_POLICY: RetryPolicy = Object.freeze({
    maxRetries: 3,
    retryableCodes: DEFAULT_RETRYABLE_CODES,
    initialBackoffMs: 100,
});

/** @internal Validates and snapshots a caller-supplied policy. */
export function validRetryPolicy(policy: RetryPolicy): RetryPolicy {
    if (!Number.isInteger(policy.maxRetries) || policy.maxRetries < 0)
        throw new InvalidArgumentError("retryPolicy.maxRetries must be a non-negative integer");
    if (!Array.isArray(policy.retryableCodes) || policy.retryableCodes.some(c => typeof c !== "number" || Code[c] === undefined))
        throw new InvalidArgumentError("retryPolicy.retryableCodes must be Code values");
    if (!Number.isFinite(policy.initialBackoffMs) || policy.initialBackoffMs <= 0)
        throw new InvalidArgumentError("retryPolicy.initialBackoffMs must be finite and positive");
    if (policy.maxBackoffMs !== undefined && (!Number.isFinite(policy.maxBackoffMs) || policy.maxBackoffMs <= 0))
        throw new InvalidArgumentError("retryPolicy.maxBackoffMs must be finite and positive");
    if (policy.onRetry !== undefined && typeof policy.onRetry !== "function")
        throw new InvalidArgumentError("retryPolicy.onRetry must be a function");
    return Object.freeze({ ...policy, retryableCodes: Object.freeze([...policy.retryableCodes]) });
}

/** @internal Whether a raw transport error is retryable under the policy. */
export function retryable(policy: RetryPolicy, error: unknown): boolean {
    return error instanceof ConnectError && policy.retryableCodes.includes(error.code);
}

/** @internal Full-jitter delay for a zero-based retry index. */
export function backoffMs(policy: RetryPolicy, retry: number): number {
    const bound = policy.initialBackoffMs * 2 ** retry;
    return Math.random() * (policy.maxBackoffMs === undefined ? bound : Math.min(bound, policy.maxBackoffMs));
}

/**
 * @internal Whether a mutation's call opted into retries. Only mutations that
 * are idempotent on replay may; any other mutation given a policy with retries
 * is rejected before any request.
 */
export function mutationRetry(policy: RetryPolicy | undefined, idempotent: boolean, operation: string): boolean {
    if (policy === undefined || validRetryPolicy(policy).maxRetries === 0)
        return false;
    if (!idempotent)
        throw new InvalidArgumentError(`${operation} cannot be retried: only touch-only writes without preconditions and schema writes are idempotent on replay`);
    return true;
}
