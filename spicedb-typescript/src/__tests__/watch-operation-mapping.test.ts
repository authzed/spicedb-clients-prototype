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
