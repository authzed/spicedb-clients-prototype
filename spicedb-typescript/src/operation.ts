import { CancelledError, DeadlineExceededError, InvalidArgumentError, toSpiceDBError } from "./errors.js";
import type { CallOptions, DeleteResult } from "./types.js";
import type { RetryPolicy } from "./retry.js";
/**
 * Local cancellation: the caller's `signal` fired, an iterator was closed, or
 * the client was closed. It is the client's {@link CancelledError} (code
 * `Canceled`), never the platform's `DOMException`, whose legacy numeric
 * `code` (20) would read like a status code. `cause` is the signal's
 * `reason`. `name` stays `"AbortError"`, so handlers written for fetch-style
 * aborts keep matching.
 */
export class OperationAbortError extends CancelledError {
    constructor(reason: unknown) { super("The operation was aborted", { cause: reason }); this.name = "AbortError"; }
}
/** @internal The paging state of one paged read, shared with its error. */
export interface PageProgress {
    /** Whether the read resumes from a string cursor, reported as `resumeCursor` on a failure. */
    readonly resumable: boolean;
    /** The cursor after everything delivered so far (initially the request's). */
    cursor?: string;
}
/** @internal A monotonic budget shared by every transport attempt, chunk, and page. */
export class Operation {
    readonly controller = new AbortController();
    private readonly deadline?: number;
    private timer?: ReturnType<typeof setTimeout>;
    private readonly external?: AbortSignal;
    private readonly onAbort: () => void;
    private failure?: Error;
    private disposed = false;
    private readonly transport?: Pick<CallOptions, "headers" | "onHeader" | "onTrailer">;
    /** The retry policy for this operation: the call's own, the operation's, or the client's. */
    readonly retryPolicy: RetryPolicy;
    /** Whether the policy came from the call or the operation, which also governs stream establishment. */
    readonly specificRetry: boolean;
    /** The client default deadline applied to each page request of a finite paged read, if any. */
    readonly pageTimeoutMs?: number;
    constructor(options: CallOptions | undefined, defaultTimeout: number | undefined, private readonly onDispose: () => void, retryPolicy: RetryPolicy, specificRetry: boolean, pageTimeoutMs?: number) {
        this.retryPolicy = retryPolicy;
        this.specificRetry = specificRetry;
        this.pageTimeoutMs = pageTimeoutMs;
        this.transport = options === undefined ? undefined : { headers: options.headers === undefined ? undefined : new Headers(options.headers), onHeader: options.onHeader, onTrailer: options.onTrailer };
        const timeout = options?.timeoutMs ?? defaultTimeout;
        if (timeout !== undefined && (!Number.isFinite(timeout) || timeout <= 0))
            throw new InvalidArgumentError("timeoutMs must be finite and positive");
        this.external = options?.signal;
        this.onAbort = () => this.fail(new OperationAbortError(this.external?.reason));
        if (this.external?.aborted)
            this.onAbort();
        else
            this.external?.addEventListener("abort", this.onAbort, { once: true });
        if (timeout !== undefined) {
            this.deadline = performance.now() + timeout;
            if (!this.failure)
                this.scheduleDeadline();
        }
    }
    private scheduleDeadline(): void {
        const remaining = this.deadline! - performance.now();
        if (remaining <= 0) {
            this.fail(new DeadlineExceededError("Operation deadline exceeded"));
            return;
        }
        this.timer = setTimeout(() => this.scheduleDeadline(), Math.min(Math.ceil(remaining), 0x7fffffff));
        this.timer.unref?.();
    }
    fail(error: Error): void {
        if (this.failure === undefined)
            this.failure = error;
        clearTimeout(this.timer);
        this.external?.removeEventListener("abort", this.onAbort);
        this.controller.abort(error);
        queueMicrotask(this.onDispose);
    }
    check(): void {
        if (this.failure)
            throw this.failure;
        if (this.deadline !== undefined && performance.now() >= this.deadline) {
            this.fail(new DeadlineExceededError("Operation deadline exceeded"));
            throw this.failure;
        }
        if (this.disposed)
            throw new OperationAbortError(undefined);
    }
    callOptions(): {
        signal: AbortSignal;
        timeoutMs?: number;
        headers?: HeadersInit;
        onHeader?: (headers: Headers) => void;
        onTrailer?: (trailers: Headers) => void;
    } {
        this.check();
        // The transport escape hatch is forwarded to every attempt, chunk and
        // page. signal/timeoutMs always come from the shared budget.
        const t = this.transport;
        return { ...(t?.headers === undefined ? {} : { headers: t.headers }), ...(t?.onHeader === undefined ? {} : { onHeader: t.onHeader }), ...(t?.onTrailer === undefined ? {} : { onTrailer: t.onTrailer }),
            signal: this.controller.signal, ...(this.deadline === undefined ? {} : { timeoutMs: Math.max(1, Math.ceil(this.deadline - performance.now())) }) };
    }
    map(error: unknown): Error {
        if (this.failure)
            return this.failure;
        return error instanceof OperationAbortError ? error : toSpiceDBError(error);
    }
    async race<T>(promise: PromiseLike<T>): Promise<T> {
        try {
            this.check();
        }
        catch (error) {
            void Promise.resolve(promise).catch(() => { });
            throw error;
        }
        const signal = this.controller.signal;
        let listener: (() => void) | undefined;
        try {
            return await Promise.race([promise, new Promise<never>((_, reject) => {
                    listener = () => reject(this.failure ?? new OperationAbortError(undefined));
                    signal.addEventListener("abort", listener, { once: true });
                    if (signal.aborted)
                        listener();
                })]);
        }
        finally {
            if (listener)
                signal.removeEventListener("abort", listener);
        }
    }
    /**
     * Awaits `promise` for at most `ms`; past that the operation fails with
     * `DeadlineExceededError`, which also cancels the transport call. The
     * operation's own deadline and signal still apply.
     */
    async within<T>(promise: PromiseLike<T>, ms: number): Promise<T> {
        const expire = () => this.fail(new DeadlineExceededError(`Page deadline exceeded: the server did not finish a page request within ${this.pageTimeoutMs} ms (the client default deadline applies to each page)`));
        if (ms <= 0)
            expire();
        const timer = setTimeout(expire, Math.min(Math.ceil(Math.max(ms, 0)), 0x7fffffff));
        timer.unref?.();
        try {
            return await this.race(promise);
        }
        finally {
            clearTimeout(timer);
        }
    }
    async sleep(ms: number): Promise<void> {
        this.check();
        let timer: ReturnType<typeof setTimeout> | undefined;
        try {
            await this.race(new Promise<void>(resolve => { timer = setTimeout(resolve, ms); }));
        }
        finally {
            clearTimeout(timer);
        }
    }
    cancel(): void { this.fail(new OperationAbortError(undefined)); this.dispose(); }
    dispose(): void {
        if (this.disposed)
            return;
        this.disposed = true;
        clearTimeout(this.timer);
        this.external?.removeEventListener("abort", this.onAbort);
        this.controller.abort();
        this.onDispose();
    }
}
/**
 * @internal Iterates one page request of a finite paged read under the
 * operation's per-page budget. Only time spent waiting on the server counts:
 * the caller's loop body between items does not. With `perMessage`, every
 * response message (an export batch) gets a fresh budget.
 */
export function pageOf<T>(op: Operation, items: AsyncIterable<T>, perMessage = false): AsyncIterable<T> {
    const budget = op.pageTimeoutMs;
    if (budget === undefined)
        return items;
    return {
        [Symbol.asyncIterator](): AsyncIterator<T> {
            const iterator = items[Symbol.asyncIterator]();
            let remaining = budget;
            return {
                async next() {
                    const started = performance.now();
                    const result = await op.within(iterator.next(), remaining);
                    remaining = perMessage ? budget : remaining - (performance.now() - started);
                    return result;
                },
                // Closing passes straight through, so `for await` releases the call as before.
                return: (value?: unknown) => iterator.return ? iterator.return(value) : Promise.resolve({ done: true, value: undefined }),
            };
        },
    };
}
/** @internal Attach the resume cursor of a failed paged read to its (client-owned) error. */
export function withResumeCursor(error: Error, progress: PageProgress | undefined): Error {
    if (progress?.resumable && progress.cursor !== undefined)
        Object.defineProperty(error, "resumeCursor", { value: progress.cursor, enumerable: true, configurable: true });
    return error;
}
/** @internal Attach acknowledged delete progress without mutating a caller-owned abort reason. */
export function withDeleteProgress(error: Error, progress: DeleteResult): Error {
    Object.defineProperty(error, "deleteProgress", { value: progress, enumerable: true });
    return error;
}
