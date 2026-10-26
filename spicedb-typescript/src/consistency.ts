import { create, clone } from "@bufbuild/protobuf";
import { type Consistency as ProtoConsistency, ConsistencySchema, ZedTokenSchema } from "@spicedb/proto";
import { InvalidArgumentError } from "./errors.js";
import { ZedToken, tokenString } from "./token.js";
/** Opaque, immutable read consistency. Use the named factories. */
export class Consistency {
    private constructor(private readonly proto: ProtoConsistency) { Object.freeze(this); }
    /** @internal */ static _wrap(proto: ProtoConsistency): Consistency { return new Consistency(clone(ConsistencySchema, proto)); }
    /** @internal */ _toProto(): ProtoConsistency { return clone(ConsistencySchema, this.proto); }
    /** @internal */ _key(): string { const r = this.proto.requirement; return `${r.case}:${typeof r.value === "object" ? r.value.token : String(r.value)}`; }
}
export function full(): Consistency { return Consistency._wrap(create(ConsistencySchema, { requirement: { case: "fullyConsistent", value: true } })); }
export function minLatency(): Consistency { return Consistency._wrap(create(ConsistencySchema, { requirement: { case: "minimizeLatency", value: true } })); }
function tokenConsistency(revision: ZedToken, mode: "atLeastAsFresh" | "atExactSnapshot"): Consistency {
    if (!(revision instanceof ZedToken) || !revision.toString())
        throw new InvalidArgumentError("revision must be a nonempty ZedToken");
    return Consistency._wrap(create(ConsistencySchema, { requirement: { case: mode, value: create(ZedTokenSchema, { token: tokenString(revision) }) } }));
}
export function atLeast(revision: ZedToken): Consistency { return tokenConsistency(revision, "atLeastAsFresh"); }
export function snapshot(revision: ZedToken): Consistency { return tokenConsistency(revision, "atExactSnapshot"); }
export function atLeastOrFull(revision: ZedToken | undefined): Consistency { return revision === undefined ? full() : atLeast(revision); }
export function atLeastOrMinLatency(revision: ZedToken | undefined): Consistency { return revision === undefined ? minLatency() : atLeast(revision); }
