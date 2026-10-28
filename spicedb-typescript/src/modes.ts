import { InvalidArgumentError } from "./errors.js";

export enum SubjectRelationKind { Any = "any", Direct = "direct", Named = "named" }
export enum Tracing { Disabled = "disabled", Enabled = "enabled" }
export enum LookupPermissionshipFilter { All = "all", HasPermissionOnly = "hasPermissionOnly" }
export enum LookupDebug { Disabled = "disabled", Enabled = "enabled" }
export enum Wildcards { Default = "default", Include = "include", Exclude = "exclude" }
export enum WatchContent { Relationships = "relationships", Checkpoints = "checkpoints", Schema = "schema" }
export enum TransportSecurity { Tls = "tls", Plaintext = "plaintext" }
export enum RemoteCredentials { LoopbackOnly = "loopbackOnly", AllowPlaintext = "allowPlaintext" }
/** Whether idempotent reads are retried with the client's retry policy. Mutations never are. */
export enum Retries { Enabled = "enabled", Disabled = "disabled" }
/** Whether opening a server stream is retried before any item is exposed. A partially consumed stream is never restarted. */
export enum StreamRetries { Enabled = "enabled", Disabled = "disabled" }
/** Whether unary operations get the client's default deadline. The caller's own `timeoutMs`/`signal` always apply. */
export enum DefaultDeadline { Enabled = "enabled", Disabled = "disabled" }
/** How `checkBulk` reports per-item failures: fail the call (default), or return them on each `CheckResult.error`. */
export enum BulkItemErrors { Fail = "fail", Returned = "returned" }
/** Whether `deleteRelationships` may delete part of the matches (default) or is all-or-nothing. */
export enum DeletePartial { Allowed = "allowed", Disallowed = "disallowed" }

/** @internal Validate JavaScript callers and future unknown mode values. */
export function mode<T extends string>(value: T | undefined, choices: Record<string, T>, fallback: T, field: string): T {
    const selected = value === undefined ? fallback : value;
    if (!Object.values(choices).includes(selected))
        throw new InvalidArgumentError(`${field}: unsupported mode ${String(selected)}`);
    return selected;
}
