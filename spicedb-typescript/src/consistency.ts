import { create } from "@bufbuild/protobuf";
import {
  type Consistency as ProtoConsistency,
  ConsistencySchema,
} from "@spicedb/proto";
import { ZedTokenSchema } from "@spicedb/proto";

/**
 * Opaque consistency strategy. Construct via {@link full}, {@link minLatency},
 * {@link atLeast}, {@link snapshot}, {@link atLeastOrFull}, or
 * {@link atLeastOrMinLatency} -- never directly.
 */
export class Consistency {
  /** @internal */
  private constructor(private readonly proto: ProtoConsistency) {}

  /** @internal */
  static _wrap(proto: ProtoConsistency): Consistency {
    return new Consistency(proto);
  }

  /** @internal */
  _toProto(): ProtoConsistency {
    return this.proto;
  }
}

/**
 * Creates a consistency requirement for fully consistent reads.
 * This is the slowest option but guarantees the most up-to-date data.
 */
export function full(): Consistency {
  return Consistency._wrap(
    create(ConsistencySchema, {
      requirement: { case: "fullyConsistent", value: true },
    }),
  );
}

/**
 * Creates a consistency requirement that minimizes latency by using
 * the fastest snapshot available.
 */
export function minLatency(): Consistency {
  return Consistency._wrap(
    create(ConsistencySchema, {
      requirement: { case: "minimizeLatency", value: true },
    }),
  );
}

/**
 * Creates a consistency requirement that ensures data is at least as fresh
 * as the given revision token.
 *
 * @param revision - An opaque revision string returned from a previous write
 */
export function atLeast(revision: string): Consistency {
  return Consistency._wrap(
    create(ConsistencySchema, {
      requirement: {
        case: "atLeastAsFresh",
        value: create(ZedTokenSchema, { token: revision }),
      },
    }),
  );
}

/**
 * Returns atLeast(revision) if revision is non-empty, otherwise full().
 * Use this when you have an optional revision from a previous write and
 * want the safest fallback.
 *
 * @param revision - An opaque revision string, or empty string for full consistency
 */
export function atLeastOrFull(revision: string): Consistency {
  if (!revision) {
    return full();
  }
  return atLeast(revision);
}

/**
 * Returns atLeast(revision) if revision is non-empty, otherwise minLatency().
 * Use this when you have an optional revision but prefer performance over
 * full consistency as the fallback.
 *
 * @param revision - An opaque revision string, or empty string for min latency
 */
export function atLeastOrMinLatency(revision: string): Consistency {
  if (!revision) {
    return minLatency();
  }
  return atLeast(revision);
}

/**
 * Creates a consistency requirement that reads data at the exact given
 * snapshot. If the snapshot is no longer available, an error is returned.
 *
 * @param revision - An opaque revision string returned from a previous write
 */
export function snapshot(revision: string): Consistency {
  return Consistency._wrap(
    create(ConsistencySchema, {
      requirement: {
        case: "atExactSnapshot",
        value: create(ZedTokenSchema, { token: revision }),
      },
    }),
  );
}
