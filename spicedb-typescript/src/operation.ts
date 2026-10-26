import { DeadlineExceededError, InvalidArgumentError, toSpiceDBError } from "./errors.js";
import type { CallOptions, DeleteResult } from "./types.js";
/** Caller cancellation stays a native DOMException and preserves signal.reason as cause. */
export class OperationAbortError extends DOMException {
    readonly cause: unknown;
    declare readonly deleteProgress?: DeleteResult;
    constructor(reason: unknown) { super("The operation was aborted", "AbortError"); this.cause = reason; }
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
    constructor(options: CallOptions | undefined, defaultTimeout: number | undefined, private readonly onDispose: () => void) {
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
    } {
        this.check();
        return { signal: this.controller.signal, ...(this.deadline === undefined ? {} : { timeoutMs: Math.max(1, Math.ceil(this.deadline - performance.now())) }) };
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
/** @internal Attach acknowledged delete progress without mutating a caller-owned abort reason. */
export function withDeleteProgress(error: Error, progress: DeleteResult): Error {
    Object.defineProperty(error, "deleteProgress", { value: progress, enumerable: true });
    return error;
}
