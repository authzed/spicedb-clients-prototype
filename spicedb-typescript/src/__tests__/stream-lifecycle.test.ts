import { describe, it, expect, vi } from "vitest";
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

// ---------------------------------------------------------------------------
// Root DESIGN.md, "RULE: Abandoning a stream must release it": a `break` in
// a `for await` loop must actually release the underlying transport, not
// just stop the loop. Connect-ES's server-streaming iterator deliberately
// omits `return()`/`throw()` (see its run-call.js, "We deliberately omit
// throw/return"), so `break` alone is provably NOT sufficient -- these
// tests assert on the CallOptions.signal the client threads to the
// transport, which is the mechanism that actually tears down the HTTP/2
// stream (verified directly against @connectrpc/connect's and
// @connectrpc/connect-node's source: runStreamingCall links CallOptions.
// signal into the request signal, and the Node HTTP/2 client destroys the
// in-flight response on that signal aborting). Asserting only "the loop
// exited" would pass identically whether or not the fix is present, since
// exiting the loop is exactly what already happened while leaking -- so
// every test here checks `signal.aborted`, not just that iteration stopped.
// ---------------------------------------------------------------------------

interface FakeProto {
  permissions?: Record<string, ReturnType<typeof vi.fn>>;
  watch?: Record<string, ReturnType<typeof vi.fn>>;
  close?: ReturnType<typeof vi.fn>;
}

function clientWithFakeProto(fake: FakeProto): SpiceDBClient {
  const client = new SpiceDBClient({
    endpoint: "localhost:50051",
    token: "test-token",
    insecure: true,
  });
  (client as unknown as { proto: FakeProto }).proto = fake;
  return client;
}

/**
 * A fake streaming proto method that captures the `CallOptions.signal` it
 * was called with, so a test can inspect it after abandoning the stream.
 * Yields `items` one at a time and never terminates on its own (mimicking a
 * long-lived/large stream that is still "in flight" when the caller stops
 * consuming), so a discriminating test doesn't depend on a race between
 * `break` and the fake stream's own natural exhaustion.
 */
function capturingStream<T>(items: T[]): {
  fn: ReturnType<typeof vi.fn>;
  capturedSignal: () => AbortSignal | undefined;
} {
  let signal: AbortSignal | undefined;
  const fn = vi.fn(
    (_req: unknown, options?: { signal?: AbortSignal }) => {
      signal = options?.signal;
      return (async function* () {
        for (const item of items) {
          yield item;
        }
        // Simulate a stream with more data than the caller ever reads.
        await new Promise(() => {
          /* never resolves */
        });
      })();
    },
  );
  return { fn, capturedSignal: () => signal };
}

describe("stream abandonment releases the transport", () => {
  it("readRelationships: break-ing out of the loop aborts the signal passed to the transport", async () => {
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
    const { fn, capturedSignal } = capturingStream([
      relResp("1"),
      relResp("2"),
      relResp("3"),
    ]);
    const client = clientWithFakeProto({
      permissions: { readRelationships: fn },
    });

    expect(capturedSignal()).toBeUndefined();

    let count = 0;
    for await (const _rel of client.readRelationships(
      { resourceType: "document" },
      full(),
    )) {
      count++;
      if (count === 1) break;
    }

    expect(count).toBe(1);
    expect(capturedSignal()).toBeDefined();
    expect(capturedSignal()?.aborted).toBe(true);
  });

  it("lookupResources: break-ing out of the loop aborts the signal passed to the transport", async () => {
    const lookupResp = (id: string) =>
      create(LookupResourcesResponseSchema, {
        resourceObjectId: id,
        permissionship: LookupPermissionship.HAS_PERMISSION,
      });
    const { fn, capturedSignal } = capturingStream([
      lookupResp("doc1"),
      lookupResp("doc2"),
    ]);
    const client = clientWithFakeProto({
      permissions: { lookupResources: fn },
    });

    for await (const _r of client.lookupResources(
      {
        resourceType: "document",
        permission: "view",
        subjectType: "user",
        subjectId: "jimmy",
      },
      full(),
    )) {
      break;
    }

    expect(capturedSignal()?.aborted).toBe(true);
  });

  it("lookupSubjects: break-ing out of the loop aborts the signal passed to the transport", async () => {
    const subjResp = (id: string) =>
      create(LookupSubjectsResponseSchema, {
        subject: create(ResolvedSubjectSchema, {
          subjectObjectId: id,
          permissionship: LookupPermissionship.HAS_PERMISSION,
        }),
      });
    const { fn, capturedSignal } = capturingStream([
      subjResp("sally"),
      subjResp("jimmy"),
    ]);
    const client = clientWithFakeProto({
      permissions: { lookupSubjects: fn },
    });

    for await (const _s of client.lookupSubjects(
      {
        resourceType: "document",
        resourceId: "readme",
        permission: "view",
        subjectType: "user",
      },
      full(),
    )) {
      break;
    }

    expect(capturedSignal()?.aborted).toBe(true);
  });

  it("exportBulkRelationships: break-ing out of the loop aborts the signal passed to the transport", async () => {
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
    const { fn, capturedSignal } = capturingStream([
      exportResp(["doc1", "doc2"]),
    ]);
    const client = clientWithFakeProto({
      permissions: { exportBulkRelationships: fn },
    });

    for await (const _r of client.exportBulkRelationships(full())) {
      break;
    }

    expect(capturedSignal()?.aborted).toBe(true);
  });

  it("watch: break-ing out of the loop aborts the signal passed to the transport", async () => {
    const watchResp = (revision: string) =>
      create(WatchResponseSchema, {
        changesThrough: create(ZedTokenSchema, { token: revision }),
      });
    const { fn, capturedSignal } = capturingStream([
      watchResp("rev1"),
      watchResp("rev2"),
    ]);
    const client = clientWithFakeProto({ watch: { watch: fn } });

    for await (const _e of client.watch()) {
      break;
    }

    expect(capturedSignal()?.aborted).toBe(true);
  });

  it("readRelationships: an externally-supplied AbortSignal is linked in and honored", async () => {
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
    const { fn, capturedSignal } = capturingStream([relResp("1")]);
    const client = clientWithFakeProto({
      permissions: { readRelationships: fn },
    });

    const external = new AbortController();
    const iter = client.readRelationships(
      { resourceType: "document" },
      full(),
      { signal: external.signal },
    );

    const { value } = await iter.next();
    expect(value).toBeDefined();
    expect(capturedSignal()?.aborted).toBe(false);

    external.abort();
    expect(capturedSignal()?.aborted).toBe(true);

    await iter.return?.(undefined);
  });
});

describe("SpiceDBClient#close", () => {
  it("delegates to the underlying proto client's close()", () => {
    const closeFn = vi.fn();
    const client = clientWithFakeProto({ close: closeFn });

    client.close();

    expect(closeFn).toHaveBeenCalledTimes(1);
  });

  it("is idempotent end-to-end -- calling it more than once does not throw", () => {
    const client = new SpiceDBClient({
      endpoint: "localhost:50051",
      token: "test-token",
      insecure: true,
    });

    expect(() => client.close()).not.toThrow();
    expect(() => client.close()).not.toThrow();
    expect(() => client.close()).not.toThrow();
  });
});
