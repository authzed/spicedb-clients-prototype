import { InvalidArgumentError } from "./errors.js";

/**
 * An immutable, opaque SpiceDB revision with lossless string serialization.
 *
 * The value is a readonly public field, so structural comparisons
 * (`expect(a).toEqual(b)`, `util.isDeepStrictEqual`) tell two different
 * tokens apart; the instance is frozen.
 */
export class ZedToken {
    /** The opaque token text, as SpiceDB sent it. */
    readonly token: string;
    constructor(value: string) {
        if (typeof value !== "string")
            throw new InvalidArgumentError("ZedToken requires a string");
        this.token = value;
        Object.freeze(this);
    }
    equals(other: ZedToken): boolean { return other instanceof ZedToken && this.token === other.token; }
    toString(): string { return this.token; }
    toJSON(): string { return this.token; }
}

/** @internal */
export function tokenString(value: ZedToken): string {
    if (!(value instanceof ZedToken))
        throw new InvalidArgumentError("revision must be a ZedToken");
    return value.toString();
}

/**
 * @internal A caller-supplied revision (consistency, watch start, permission-set
 * revision): an empty or whitespace-only token is a local validation error,
 * never sent for the server to reject.
 */
export function requestToken(value: ZedToken, what = "revision"): string {
    const token = tokenString(value);
    if (token.trim() === "")
        throw new InvalidArgumentError(`${what} must be a nonempty ZedToken, not empty or whitespace`);
    return token;
}

/** @internal An absent required wire revision retains its empty representation. */
export function serverToken(value?: string): ZedToken { return new ZedToken(value ?? ""); }
