import { describe, it, expect, vi } from "vitest";
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import {
  ReadRelationshipsResponseSchema,
  LookupResourcesResponseSchema,
  LookupSubjectsResponseSchema,
  ExportBulkRelationshipsResponseSchema,
  WatchResponseSchema,
  RelationshipSchema,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
  ResolvedSubjectSchema,
  ZedTokenSchema,
  LookupPermissionship,
} from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full } from "../consistency.js";
import { UnavailableError } from "../errors.js";

// ---------------------------------------------------------------------------
// Test harness: SpiceDBClient doesn't accept a proto client via its public
// constructor (by design — see SpiceDBClientOptions), so these tests replace
// the private `proto` field at runtime with a fake whose streaming methods
// are `vi.fn()`s we control. TypeScript's `private`/`readonly` are
// compile-time only, so this is safe and doesn't require touching the
// client's public API.
// ---------------------------------------------------------------------------

interface FakeProto {
  permissions?: Record<string, ReturnType<typeof vi.fn>>;
  watch?: Record<string, ReturnType<typeof vi.fn>>;
}

function clientWithFakeProto(fake: FakeProto): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
    maxRetries: 3,
  });
  (client as unknown as { proto: FakeProto }).proto = fake;
  return client;
}

/**
 * Builds a `vi.fn()` standing in for a streaming proto method. Each call
 * consumes the next `behaviors` entry (the last entry repeats once
 * exhausted): it yields `items` one at a time, then throws `error` if set.
 * Mirrors a stream that fails on re-establishment or drains successfully.
 */
function flakyStream<T>(
  behaviors: Array<{ items: T[]; error?: unknown }>,
): ReturnType<typeof vi.fn> {
  let callCount = 0;
  return vi.fn(() => {
    const behavior = behaviors[Math.min(callCount, behaviors.length - 1)];
    callCount++;
    return (async function* () {
      for (const item of behavior.items) {
        yield item;
      }
      if (behavior.error) {
        throw behavior.error;
      }
    })();
  });
}

async function collect<T>(iter: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = [];
  for await (const item of iter) {
    out.push(item);
  }
  return out;
}

const transientErr = () => new ConnectError("unavailable", Code.Unavailable);
const permanentErr = () =>
  new ConnectError("bad request", Code.InvalidArgument);

describe("streaming RPC establishment retry (no replay)", () => {
  // -------------------------------------------------------------------------
  // readRelationships
  // -------------------------------------------------------------------------
  describe("readRelationships", () => {
    const relResp = (id: string) =>
      create(ReadRelationshipsResponseSchema, {
        relationship: create(RelationshipSchema, {
          resource: create(ObjectReferenceSchema, {
            objectType: "document",
            objectId: id,
          }),
          relation: "viewer",
          subject: create(SubjectReferenceSchema, {
            object: create(ObjectReferenceSchema, {
              objectType: "user",
              objectId: "jimmy",
            }),
          }),
        }),
      });

    it("retries stream establishment on a transient error before any item is yielded", async () => {
      const fn = flakyStream([
        { items: [], error: transientErr() },
        { items: [relResp("doc1"), relResp("doc2")] },
      ]);
      const client = clientWithFakeProto({
        permissions: { readRelationships: fn },
      });

      const results = await collect(
        client.readRelationships({ resourceType: "document" }, full()),
      );

      expect(results.map((r) => r.resourceId)).toEqual(["doc1", "doc2"]);
      expect(fn).toHaveBeenCalledTimes(2);
    });

    it("does NOT retry (and does not replay) a transient error after an item has been yielded", async () => {
      const fn = flakyStream([
        { items: [relResp("doc1")], error: transientErr() },
      ]);
      const client = clientWithFakeProto({
        permissions: { readRelationships: fn },
      });

      const seen: string[] = [];
      await expect(
        (async () => {
          for await (const rel of client.readRelationships(
            { resourceType: "document" },
            full(),
          )) {
            seen.push(rel.resourceId);
          }
        })(),
      ).rejects.toBeInstanceOf(UnavailableError);

      expect(seen).toEqual(["doc1"]);
      expect(fn).toHaveBeenCalledTimes(1);
    });

    it("does not retry a non-transient error, even with zero items yielded", async () => {
      const fn = flakyStream([{ items: [], error: permanentErr() }]);
      const client = clientWithFakeProto({
        permissions: { readRelationships: fn },
      });

      await expect(
        collect(client.readRelationships({ resourceType: "document" }, full())),
      ).rejects.toThrow();
      expect(fn).toHaveBeenCalledTimes(1);
    });
  });

  // -------------------------------------------------------------------------
  // lookupResources
  // -------------------------------------------------------------------------
  describe("lookupResources", () => {
    const lookupResp = (id: string) =>
      create(LookupResourcesResponseSchema, {
        resourceObjectId: id,
        permissionship: LookupPermissionship.HAS_PERMISSION,
      });

    it("retries stream establishment on a transient error before any item is yielded", async () => {
      const fn = flakyStream([
        { items: [], error: transientErr() },
        { items: [lookupResp("doc1"), lookupResp("doc2")] },
      ]);
      const client = clientWithFakeProto({
        permissions: { lookupResources: fn },
      });

      const results = await collect(
        client.lookupResources(
          {
            resourceType: "document",
            permission: "view",
            subjectType: "user",
            subjectId: "jimmy",
          },
          full(),
        ),
      );

      expect(results.map((r) => r.resourceId)).toEqual(["doc1", "doc2"]);
      expect(fn).toHaveBeenCalledTimes(2);
    });

    it("does NOT retry (and does not replay) a transient error after an item has been yielded", async () => {
      const fn = flakyStream([
        { items: [lookupResp("doc1")], error: transientErr() },
      ]);
      const client = clientWithFakeProto({
        permissions: { lookupResources: fn },
      });

      const seen: string[] = [];
      await expect(
        (async () => {
          for await (const r of client.lookupResources(
            {
              resourceType: "document",
              permission: "view",
              subjectType: "user",
              subjectId: "jimmy",
            },
            full(),
          )) {
            seen.push(r.resourceId);
          }
        })(),
      ).rejects.toBeInstanceOf(UnavailableError);

      expect(seen).toEqual(["doc1"]);
      expect(fn).toHaveBeenCalledTimes(1);
    });
  });

  // -------------------------------------------------------------------------
  // lookupSubjects
  // -------------------------------------------------------------------------
  describe("lookupSubjects", () => {
    const subjResp = (id: string) =>
      create(LookupSubjectsResponseSchema, {
        subject: create(ResolvedSubjectSchema, {
          subjectObjectId: id,
          permissionship: LookupPermissionship.HAS_PERMISSION,
        }),
      });

    it("retries stream establishment on a transient error before any item is yielded", async () => {
      const fn = flakyStream([
        { items: [], error: transientErr() },
        { items: [subjResp("sally"), subjResp("jimmy")] },
      ]);
      const client = clientWithFakeProto({
        permissions: { lookupSubjects: fn },
      });

      const results = await collect(
        client.lookupSubjects(
          {
            resourceType: "document",
            resourceId: "readme",
            permission: "view",
            subjectType: "user",
          },
          full(),
        ),
      );

      expect(results.map((r) => r.subject.subjectId)).toEqual([
        "sally",
        "jimmy",
      ]);
      expect(fn).toHaveBeenCalledTimes(2);
    });

    it("does NOT retry (and does not replay) a transient error after an item has been yielded", async () => {
      const fn = flakyStream([
        { items: [subjResp("sally")], error: transientErr() },
      ]);
      const client = clientWithFakeProto({
        permissions: { lookupSubjects: fn },
      });

      const seen: string[] = [];
      await expect(
        (async () => {
          for await (const s of client.lookupSubjects(
            {
              resourceType: "document",
              resourceId: "readme",
              permission: "view",
              subjectType: "user",
            },
            full(),
          )) {
            seen.push(s.subject.subjectId);
          }
        })(),
      ).rejects.toBeInstanceOf(UnavailableError);

      expect(seen).toEqual(["sally"]);
      expect(fn).toHaveBeenCalledTimes(1);
    });
  });

  // -------------------------------------------------------------------------
  // exportBulkRelationships
  // -------------------------------------------------------------------------
  describe("exportBulkRelationships", () => {
    const exportResp = (ids: string[]) =>
      create(ExportBulkRelationshipsResponseSchema, {
        relationships: ids.map((id) =>
          create(RelationshipSchema, {
            resource: create(ObjectReferenceSchema, {
              objectType: "document",
              objectId: id,
            }),
            relation: "viewer",
            subject: create(SubjectReferenceSchema, {
              object: create(ObjectReferenceSchema, {
                objectType: "user",
                objectId: "jimmy",
              }),
            }),
          }),
        ),
      });

    it("retries stream establishment on a transient error before any item is yielded", async () => {
      const fn = flakyStream([
        { items: [], error: transientErr() },
        { items: [exportResp(["doc1", "doc2"])] },
      ]);
      const client = clientWithFakeProto({
        permissions: { exportBulkRelationships: fn },
      });

      const results = await collect(client.exportBulkRelationships(full()));

      expect(results.map((r) => r.resourceId)).toEqual(["doc1", "doc2"]);
      expect(fn).toHaveBeenCalledTimes(2);
    });

    it("does NOT retry (and does not replay) a transient error after an item has been yielded", async () => {
      const fn = flakyStream([
        { items: [exportResp(["doc1"])], error: transientErr() },
      ]);
      const client = clientWithFakeProto({
        permissions: { exportBulkRelationships: fn },
      });

      const seen: string[] = [];
      await expect(
        (async () => {
          for await (const r of client.exportBulkRelationships(full())) {
            seen.push(r.resourceId);
          }
        })(),
      ).rejects.toBeInstanceOf(UnavailableError);

      expect(seen).toEqual(["doc1"]);
      expect(fn).toHaveBeenCalledTimes(1);
    });
  });

  // -------------------------------------------------------------------------
  // watch
  // -------------------------------------------------------------------------
  describe("watch", () => {
    const watchResp = (revision: string) =>
      create(WatchResponseSchema, {
        changesThrough: create(ZedTokenSchema, { token: revision }),
      });

    it("retries stream establishment on a transient error before any event is yielded", async () => {
      const fn = flakyStream([
        { items: [], error: transientErr() },
        { items: [watchResp("rev1"), watchResp("rev2")] },
      ]);
      const client = clientWithFakeProto({ watch: { watch: fn } });

      const results = await collect(client.watch());

      expect(results.map((e) => e.revision)).toEqual(["rev1", "rev2"]);
      expect(fn).toHaveBeenCalledTimes(2);
    });

    it("does NOT retry (and does not replay) a transient error after an event has been yielded — never retries mid-watch", async () => {
      const fn = flakyStream([
        { items: [watchResp("rev1")], error: transientErr() },
      ]);
      const client = clientWithFakeProto({ watch: { watch: fn } });

      const seen: string[] = [];
      await expect(
        (async () => {
          for await (const e of client.watch()) {
            seen.push(e.revision);
          }
        })(),
      ).rejects.toBeInstanceOf(UnavailableError);

      expect(seen).toEqual(["rev1"]);
      expect(fn).toHaveBeenCalledTimes(1);
    });
  });

  // -------------------------------------------------------------------------
  // Shared retry budget: exhausts maxRetries then surfaces the transient
  // error natively (mirrors withRetry's unary exhaustion behavior).
  // -------------------------------------------------------------------------
  it("gives up establishment retries once maxRetries is exhausted", async () => {
    const fn = flakyStream([{ items: [], error: transientErr() }]);
    const client = new SpiceDBClient({
      endpoint: "localhost:50051",
      token: "test-token",
      insecure: true,
      maxRetries: 2,
    });
    (client as unknown as { proto: FakeProto }).proto = {
      permissions: { readRelationships: fn },
    };

    await expect(
      collect(client.readRelationships({ resourceType: "document" }, full())),
    ).rejects.toBeInstanceOf(UnavailableError);

    // 1 initial attempt + 2 retries = 3 total establishment attempts.
    expect(fn).toHaveBeenCalledTimes(3);
  });
});
