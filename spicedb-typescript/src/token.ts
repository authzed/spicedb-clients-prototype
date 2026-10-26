import { InvalidArgumentError } from "./errors.js";

/** An immutable, opaque SpiceDB revision with lossless string serialization. */
export class ZedToken {
    readonly #value: string;
    constructor(value: string) {
        if (typeof value !== "string")
            throw new InvalidArgumentError("ZedToken requires a string");
        this.#value = value;
        Object.freeze(this);
    }
    equals(other: ZedToken): boolean { return other instanceof ZedToken && this.#value === other.#value; }
    toString(): string { return this.#value; }
    toJSON(): string { return this.#value; }
}

/** @internal */
export function tokenString(value: ZedToken): string {
    if (!(value instanceof ZedToken))
        throw new InvalidArgumentError("revision must be a ZedToken");
    return value.toString();
}

/** @internal An absent required wire revision retains its empty representation. */
export function serverToken(value?: string): ZedToken { return new ZedToken(value ?? ""); }
