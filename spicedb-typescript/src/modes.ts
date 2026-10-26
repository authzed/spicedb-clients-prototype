import { InvalidArgumentError } from "./errors.js";

export enum SubjectRelationKind { Any = "any", Direct = "direct", Named = "named" }
export enum Tracing { Disabled = "disabled", Enabled = "enabled" }
export enum LookupPermissionshipFilter { All = "all", HasPermissionOnly = "hasPermissionOnly" }
export enum LookupDebug { Disabled = "disabled", Enabled = "enabled" }
export enum Wildcards { Default = "default", Include = "include", Exclude = "exclude" }
export enum WatchContent { Relationships = "relationships", Checkpoints = "checkpoints", Schema = "schema" }
export enum TransportSecurity { Tls = "tls", Plaintext = "plaintext" }
export enum RemoteCredentials { LoopbackOnly = "loopbackOnly", AllowPlaintext = "allowPlaintext" }

/** @internal Validate JavaScript callers and future unknown mode values. */
export function mode<T extends string>(value: T | undefined, choices: Record<string, T>, fallback: T, field: string): T {
    const selected = value === undefined ? fallback : value;
    if (!Object.values(choices).includes(selected))
        throw new InvalidArgumentError(`${field}: unsupported mode ${String(selected)}`);
    return selected;
}
