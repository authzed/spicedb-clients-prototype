import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  WatchResponseSchema,
  RelationshipUpdateSchema,
  RelationshipSchema,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
  ZedTokenSchema,
  RelationshipUpdate_Operation,
  WatchKind,
  type WatchRequest,
} from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import type { WatchChange } from "../types.js";

// ---------------------------------------------------------------------------
// SpiceDBClient doesn't accept a proto client via its public constructor (by
// design — see SpiceDBClientOptions), so this replaces the private `proto`
// field at runtime with a fake. TypeScript's `private`/`readonly` are
// compile-time only. Mirrors the harness in streaming-retry.test.ts.
// ---------------------------------------------------------------------------
function clientWithFakeWatch(
  responses: ReturnType<typeof create>[],
): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: unknown }).proto = {
    watch: {
      watch: vi.fn(() =>
        (async function* () {
          for (const r of responses) {
            yield r;
          }
        })(),
      ),
    },
  };
  return client;
}

/**
 * Like `clientWithFakeWatch`, but also returns the `vi.fn()` mock so a test
 * can assert on what was actually sent to the wire (`mock.calls[n][0]` is
 * the built `WatchRequest`), not just that the call succeeded.
 */
function clientWithFakeWatchCapturingRequest(
  responses: ReturnType<typeof create>[],
): { client: SpiceDBClient; watchMock: ReturnType<typeof vi.fn> } {
  const watchMock = vi.fn(() =>
    (async function* () {
      for (const r of responses) {
        yield r;
      }
    })(),
  );
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: unknown }).proto = {
    watch: { watch: watchMock },
  };
  return { client, watchMock };
}

function relProto(subjectId: string) {
  return create(RelationshipSchema, {
    resource: create(ObjectReferenceSchema, {
      objectType: "document",
      objectId: "readme",
    }),
    relation: "viewer",
    subject: create(SubjectReferenceSchema, {
      object: create(ObjectReferenceSchema, {
        objectType: "user",
        objectId: subjectId,
      }),
    }),
  });
}

function watchResp(operation: number, subjectId: string) {
  return create(WatchResponseSchema, {
    changesThrough: create(ZedTokenSchema, { token: "rev1" }),
    updates: [
      create(RelationshipUpdateSchema, {
        operation,
        relationship: relProto(subjectId),
      }),
    ],
  });
}

/**
 * An unrecognized watch operation — `OPERATION_UNSPECIFIED`, or a future wire
 * value added after this client shipped — must map to `"unspecified"`, never
 * to `"touch"`.
 *
 * Before this fix `"touch"` was the switch's `default` arm rather than a case,
 * so any operation the client could not interpret was reported as a write. A
 * cache or index mirror consuming the watch stream would upsert a relationship
 * on an update it did not understand — one that may in fact have been a
 * delete. Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning
 * must fail", clause 2: server-supplied values the client does not recognise
 * MUST NOT raise, and MUST map to the safe, non-permissive default — never a
 * grant, and never a write.
 */
describe("watch operation mapping", () => {
  it("maps OPERATION_UNSPECIFIED to 'unspecified', not 'touch'", async () => {
    const client = clientWithFakeWatch([
      watchResp(RelationshipUpdate_Operation.UNSPECIFIED, "alice"),
    ]);

    const changes: WatchChange[] = [];
    for await (const event of client.watch()) {
      changes.push(...event.changes);
    }

    expect(changes).toHaveLength(1);
    expect(changes[0].operation).toBe("unspecified");
    expect(changes[0].operation).not.toBe("touch");
    expect(changes[0].relationship.subjectId).toBe("alice");
  });

  it("maps an unknown future operation value to 'unspecified', not 'touch'", async () => {
    // A discriminant no version of this client knows about, standing in for an
    // operation added to the proto after this client shipped.
    const client = clientWithFakeWatch([watchResp(9999, "bob")]);

    const changes: WatchChange[] = [];
    for await (const event of client.watch()) {
      changes.push(...event.changes);
    }

    expect(changes).toHaveLength(1);
    expect(changes[0].operation).toBe("unspecified");
    expect(changes[0].relationship.subjectId).toBe("bob");
  });

  it("still maps the three recognized operations to themselves", async () => {
    const client = clientWithFakeWatch([
      watchResp(RelationshipUpdate_Operation.CREATE, "carol"),
      watchResp(RelationshipUpdate_Operation.TOUCH, "dave"),
      watchResp(RelationshipUpdate_Operation.DELETE, "erin"),
    ]);

    const changes: WatchChange[] = [];
    for await (const event of client.watch()) {
      changes.push(...event.changes);
    }

    expect(changes.map((c) => c.operation)).toEqual([
      "create",
      "touch",
      "delete",
    ]);
    expect(changes.map((c) => c.relationship.subjectId)).toEqual([
      "carol",
      "dave",
      "erin",
    ]);
  });
});

/**
 * A watch stream that dies cannot be correctly resumed unless the client
 * surfaces `changes_through` (proto: "This token can be used in a
 * subsequent WatchRequest to resume watching from this point"), and cannot
 * survive an idle-timeout proxy unless the client can request
 * WATCH_KIND_INCLUDE_CHECKPOINTS. These tests exercise both.
 */
describe("watch resumability", () => {
  it("exposes a usable resume token on a change-carrying event", async () => {
    const client = clientWithFakeWatch([
      watchResp(RelationshipUpdate_Operation.TOUCH, "alice"),
    ]);

    const [event] = await (async () => {
      const events = [];
      for await (const e of client.watch()) events.push(e);
      return events;
    })();

    expect(event.revision).toBe("rev1");
  });

  it("does not request any update kinds by default", async () => {
    const { client, watchMock } = clientWithFakeWatchCapturingRequest([]);

    for await (const _e of client.watch()) {
      // no responses
    }

    expect(watchMock).toHaveBeenCalledTimes(1);
    const sentRequest = watchMock.mock.calls[0][0] as WatchRequest;
    expect(sentRequest.optionalUpdateKinds).toEqual([]);
  });

  it("includeCheckpoints reaches the wire as WATCH_KIND_INCLUDE_CHECKPOINTS", async () => {
    const { client, watchMock } = clientWithFakeWatchCapturingRequest([]);

    for await (const _e of client.watch({ includeCheckpoints: true })) {
      // no responses
    }

    expect(watchMock).toHaveBeenCalledTimes(1);
    const sentRequest = watchMock.mock.calls[0][0] as WatchRequest;
    expect(sentRequest.optionalUpdateKinds).toContain(
      WatchKind.INCLUDE_CHECKPOINTS,
    );
    // Requesting checkpoints must not silently drop relationship updates --
    // optional_update_kinds is empty-means-default, so a non-empty list is
    // the exact set requested.
    expect(sentRequest.optionalUpdateKinds).toContain(
      WatchKind.INCLUDE_RELATIONSHIP_UPDATES,
    );
  });

  it("a checkpoint event is distinguishable from an event carrying updates", async () => {
    const checkpoint = create(WatchResponseSchema, {
      changesThrough: create(ZedTokenSchema, { token: "checkpoint-rev" }),
      isCheckpoint: true,
    });
    const update = watchResp(RelationshipUpdate_Operation.TOUCH, "frank");
    const client = clientWithFakeWatch([checkpoint, update]);

    const events = [];
    for await (const e of client.watch({ includeCheckpoints: true })) {
      events.push(e);
    }

    expect(events).toHaveLength(2);
    expect(events[0].isCheckpoint).toBe(true);
    expect(events[0].changes).toEqual([]);
    expect(events[0].revision).toBe("checkpoint-rev");

    expect(events[1].isCheckpoint).toBe(false);
    expect(events[1].changes).toHaveLength(1);
  });
});
