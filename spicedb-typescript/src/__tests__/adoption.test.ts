// Adoption items from the zed/cloud-api/SDBE trial migrations (root DESIGN.md:
// "Keep protocol access secondary and ownership explicit", "Make native values
// observable and testable without the server", "Expose native errors without
// losing server diagnostics", retry/deadline/import/paging/delete rules).
import { describe, it, expect, vi } from "vitest";
import { ConnectError, Code, createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import {
    PermissionsService, SchemaService, ReadSchemaResponseSchema, ReadRelationshipsResponseSchema, LookupResourcesResponseSchema,
    CheckBulkPermissionsResponseSchema, CheckBulkPermissionsPairSchema, CheckBulkPermissionsResponseItemSchema, ImportBulkRelationshipsResponseSchema,
    DeleteRelationshipsResponseSchema, DeleteRelationshipsResponse_DeletionProgress, ExportBulkRelationshipsResponseSchema, LookupPermissionship,
    CheckPermissionResponseSchema, CheckPermissionResponse_Permissionship, ZedTokenSchema, CursorSchema, type ReadRelationshipsRequest as ProtoReadRequest,
    type LookupResourcesRequest as ProtoLookupRequest, type DeleteRelationshipsRequest as ProtoDeleteRequest,
} from "@spicedb/proto";
import {
    SpiceDBClient, Transaction, relationship, relationshipFromTuple, relationshipToString, filterToString, filterMatches, objectRefToString, subjectRefToString,
    full, atLeast, ZedToken, SpiceDBError, NotFoundError, FailedPreconditionError, InvalidArgumentError, DeadlineExceededError, AlreadyExistsError, UnavailableError,
    errorCode, toConnectError, Retries, StreamRetries, DefaultDeadline, BulkItemErrors, DeletePartial, DEFAULT_RETRY_POLICY, SubjectRelationKind, OperationAbortError,
    InMemorySpiceDB, protoconv, CheckResult, type RelationshipFilter, type RetryEvent,
} from "../index.js";

const docViewer = (doc: string, user: string) => relationship(`document:${doc}`, "viewer", `user:${user}`);
const wireRel = (doc: string) => ({ resource: { objectType: "document", objectId: doc }, relation: "viewer", subject: { object: { objectType: "user", objectId: "u" }, optionalRelation: "" } });
const checkReq = (id: string) => ({ resource: { objectType: "document", objectId: id }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() });

describe("N1/N2/N5 error interop", () => {
    it("gives every native error a Connect code, including local and caller-built errors", () => {
        expect(new InvalidArgumentError("bad").code).toBe(Code.InvalidArgument);
        expect(new DeadlineExceededError("late").code).toBe(Code.DeadlineExceeded);
        expect(new SpiceDBError("x").code).toBe(Code.Unknown);
        const built = SpiceDBError.fromCode(Code.FailedPrecondition, "guard failed");
        expect(built).toBeInstanceOf(FailedPreconditionError);
        expect(built.code).toBe(Code.FailedPrecondition);
        expect(errorCode(built)).toBe(Code.FailedPrecondition);
        expect(toConnectError(built).code).toBe(Code.FailedPrecondition);
        expect(SpiceDBError.fromCode(Code.Aborted, "retry me")).toBeInstanceOf(UnavailableError);
        expect(SpiceDBError.fromCode(Code.Aborted, "retry me").code).toBe(Code.Aborted);
    });
    it("classifies raw ConnectErrors and platform aborts", () => {
        expect(errorCode(new ConnectError("nope", Code.NotFound))).toBe(Code.NotFound);
        expect(errorCode(new OperationAbortError("stop"))).toBe(Code.Canceled);
        expect(errorCode(new Error("x"))).toBe(Code.Unknown);
        expect(toConnectError(new OperationAbortError("stop")).code).toBe(Code.Canceled);
        const native = SpiceDBError.from(new ConnectError("missing", Code.NotFound));
        expect(native).toBeInstanceOf(NotFoundError);
    });
    it("keeps the server message verbatim and returns the server's ConnectError", async () => {
        const wire = new ConnectError("relation `x` not found", Code.FailedPrecondition, { "x-request-id": "r1" });
        const client = SpiceDBClient.fromTransport(createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { checkPermission() { throw wire; } });
        }));
        const error = await client.check(checkReq("a")).catch(e => e) as SpiceDBError;
        expect(error).toBeInstanceOf(FailedPreconditionError);
        expect(error.serverMessage).toBe("relation `x` not found");
        expect(error.message).toContain("relation `x` not found");
        expect(error.toConnectError().code).toBe(Code.FailedPrecondition);
        expect(error.toConnectError().metadata.get("x-request-id")).toBe("r1");
        client.close();
    });
});

describe("N2 filterMatches", () => {
    const rel = relationship("document:readme", "viewer", "team:eng#member");
    it.each<[RelationshipFilter, boolean]>([
        [{ resourceType: "document" }, true],
        [{ resourceType: "folder" }, false],
        [{ resourceType: "document", resourceId: "readme" }, true],
        [{ resourceType: "document", resourceId: "other" }, false],
        [{ resourceType: "document", resourceIdPrefix: "read" }, true],
        [{ resourceType: "document", resourceIdPrefix: "x" }, false],
        [{ resourceType: "document", relation: "editor" }, false],
        [{ resourceType: "document", subject: { subjectType: "team" } }, true],
        [{ resourceType: "document", subject: { subjectType: "team", subjectId: "eng", relation: { kind: SubjectRelationKind.Any } } }, true],
        [{ resourceType: "document", subject: { subjectType: "team", relation: { kind: SubjectRelationKind.Direct } } }, false],
        [{ resourceType: "document", subject: { subjectType: "team", relation: { kind: SubjectRelationKind.Named, relation: "member" } } }, true],
        [{ resourceType: "document", subject: { subjectType: "team", relation: { kind: SubjectRelationKind.Named, relation: "admin" } } }, false],
        [{ resourceType: "document", subject: { subjectType: "user" } }, false],
    ])("%j -> %s", (filter, want) => expect(filterMatches(filter, rel)).toBe(want));
    it("matches direct subjects only for Direct", () => {
        expect(filterMatches({ resourceType: "document", subject: { subjectType: "user", relation: { kind: SubjectRelationKind.Direct } } }, docViewer("a", "b"))).toBe(true);
    });
});

describe("N4/N17 observable values and tuple syntax", () => {
    // Canonical output of SpiceDB's own tuple.String(tuple.Parse(s)) for the
    // corpus in spicedb-gen/tuplecompat (generated with SpiceDB v1.54's parser).
    const spicedbFormatted = [
        "document:readme#viewer@user:alice",
        "document:readme#viewer@team:eng#member",
        "document:readme#viewer@user:*",
        "document:readme#viewer@user:alice[only_name]",
        "document:readme#viewer@user:alice[ip_range:{\"cidr\":\"10.0.0.0/8\", \"list\":[\"a\", \"]\"], \"n\":1.5, \"nested\":{\"b\":true}}]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]",
        "document:readme#viewer@team:eng#member[ip_range:{\"cidr\":\"x\"}][expiration:2030-01-02T03:04:05Z]",
        "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.12Z]",
    ];
    it.each(spicedbFormatted)("parses SpiceDB's formatting and round-trips %s", text => {
        const rel = relationshipFromTuple(text);
        const ours = relationshipToString(rel);
        expect(relationshipFromTuple(ours)).toEqual(rel);
        expect(relationshipToString(relationshipFromTuple(ours))).toBe(ours);
    });
    it("formats the full syntax with sorted context and trimmed UTC expiration", () => {
        const rel = relationshipFromTuple("document:readme#viewer@team:eng#member[ip_range:{\"z\":1,\"a\":{\"y\":true,\"b\":null}}][expiration:2030-01-02T03:04:05.120Z]");
        expect(rel.subject).toEqual({ object: { objectType: "team", objectId: "eng" }, relation: "member" });
        expect(rel.caveat).toEqual({ name: "ip_range", context: { z: 1, a: { y: true, b: null } } });
        expect(relationshipToString(rel)).toBe("document:readme#viewer@team:eng#member[ip_range:{\"a\":{\"b\":null,\"y\":true},\"z\":1}][expiration:2030-01-02T03:04:05.12Z]");
        expect(relationshipFromTuple("document:a#viewer@user:b#...").subject.relation).toBeUndefined();
        expect(relationshipToString({ ...docViewer("a", "b"), caveat: { name: "c", context: {} } })).toBe("document:a#viewer@user:b[c]");
    });
    it.each(["document:a#viewer", "document:a@user:b", "document#viewer@user:b", "document:a#viewer@user:b[c:{]", "document:a#viewer@user:b[expiration:soon]", "document:a#viewer@user:b[c][d]"])("rejects %s", text => {
        expect(() => relationshipFromTuple(text)).toThrow(InvalidArgumentError);
    });
    it("formats refs, filters, transactions and consistency", () => {
        expect(objectRefToString({ objectType: "user", objectId: "a" })).toBe("user:a");
        expect(subjectRefToString({ object: { objectType: "team", objectId: "a" }, relation: "member" })).toBe("team:a#member");
        expect(filterToString({ resourceType: "document" })).toBe("document:*#*@*");
        expect(filterToString({ resourceType: "document", resourceIdPrefix: "doc-", relation: "viewer", subject: { subjectType: "user", relation: { kind: SubjectRelationKind.Direct } } })).toBe("document:doc-%*#viewer@user:*#...");
        const txn = new Transaction().mustNotMatch({ resourceType: "organization", resourceId: "acme", relation: "admin" }).delete(docViewer("a", "b")).touch(docViewer("c", "d"));
        expect(txn.length).toBe(2);
        expect(txn.toString()).toBe("[MUST_NOT_MATCH(organization:acme#admin@*), DELETE(document:a#viewer@user:b), TOUCH(document:c#viewer@user:d)]");
        const json = JSON.parse(JSON.stringify(txn));
        expect(json.updates[1]).toEqual({ operation: "touch", relationship: { resource: { objectType: "document", objectId: "c" }, relation: "viewer", subject: { object: { objectType: "user", objectId: "d" } } } });
        expect(json.preconditions[0].operation).toBe("mustNotMatch");
        expect(String(full())).toBe("full()");
        expect(JSON.stringify({ c: atLeast(new ZedToken("t1")) })).toBe("{\"c\":\"atLeast(t1)\"}");
    });
    it("drops paging state when a read item is written back", () => {
        const item = { ...docViewer("a", "b"), afterResultCursor: "cur", readAt: new ZedToken("r") };
        expect(JSON.stringify(new Transaction().delete(item).updates[0].relationship)).not.toContain("cur");
    });
});

describe("N6 fromTransport", () => {
    it("uses a caller-owned transport, applies behavior options, and leaves the transport open on close", async () => {
        let calls = 0;
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(SchemaService, { readSchema() { calls++; return create(ReadSchemaResponseSchema, { schemaText: "definition user {}", readAt: create(ZedTokenSchema, { token: "r" }) }); } });
        });
        const first = SpiceDBClient.fromTransport(transport, { defaultDeadline: DefaultDeadline.Disabled });
        expect((await first.readSchema()).schema).toBe("definition user {}");
        first.close();
        const second = SpiceDBClient.fromTransport(transport);
        expect((await second.readSchema()).revision.toString()).toBe("r");
        expect(calls).toBe(2);
        second.close();
    });
    it("rejects connection options", () => {
        const transport = createRouterTransport(() => {});
        expect(() => SpiceDBClient.fromTransport(transport, { token: "secret" } as never)).toThrow(InvalidArgumentError);
        expect(() => SpiceDBClient.fromTransport({} as never)).toThrow(InvalidArgumentError);
    });
});

describe("N9/N10 retry policy and default deadline", () => {
    function flakySchema(failures: Code[]) {
        let calls = 0;
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(SchemaService, { readSchema() { const code = failures[calls++]; if (code !== undefined) throw new ConnectError("flaky", code); return create(ReadSchemaResponseSchema, { schemaText: "ok" }); } });
        });
        return { transport, calls: () => calls };
    }
    it("never retries ResourceExhausted by default", async () => {
        const { transport, calls } = flakySchema([Code.ResourceExhausted]);
        await expect(SpiceDBClient.fromTransport(transport).readSchema()).rejects.toMatchObject({ code: Code.ResourceExhausted });
        expect(calls()).toBe(1);
    });
    it("retries caller-chosen codes and reports each retry", async () => {
        const { transport, calls } = flakySchema([Code.ResourceExhausted, Code.Internal]);
        const events: RetryEvent[] = [];
        const client = SpiceDBClient.fromTransport(transport, { retryPolicy: { maxRetries: 2, retryableCodes: [Code.ResourceExhausted, Code.Internal], initialBackoffMs: 1, onRetry: e => events.push(e) } });
        expect((await client.readSchema()).schema).toBe("ok");
        expect(calls()).toBe(3);
        expect(events.map(e => [e.operation, e.attempt, e.error.code])).toEqual([["readSchema", 1, Code.ResourceExhausted], ["readSchema", 2, Code.Internal]]);
    });
    it("Retries.Disabled makes one attempt", async () => {
        const { transport, calls } = flakySchema([Code.Unavailable]);
        await expect(SpiceDBClient.fromTransport(transport, { retries: Retries.Disabled }).readSchema()).rejects.toBeInstanceOf(UnavailableError);
        expect(calls()).toBe(1);
        expect(() => SpiceDBClient.fromTransport(transport, { retries: Retries.Disabled, retryPolicy: DEFAULT_RETRY_POLICY })).toThrow(InvalidArgumentError);
        expect(() => SpiceDBClient.fromTransport(transport, { retryPolicy: { ...DEFAULT_RETRY_POLICY, initialBackoffMs: 0 } })).toThrow(InvalidArgumentError);
    });
    it("never replays a mutation, whatever the policy", async () => {
        let calls = 0;
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { writeRelationships() { calls++; throw new ConnectError("busy", Code.Unavailable); } });
        });
        const client = SpiceDBClient.fromTransport(transport, { retryPolicy: { ...DEFAULT_RETRY_POLICY, initialBackoffMs: 1 } });
        await expect(client.write(new Transaction().touch(docViewer("a", "b")))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls).toBe(1);
    });
    it("StreamRetries.Disabled opens a stream once", async () => {
        let calls = 0;
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async *readRelationships() { calls++; throw new ConnectError("down", Code.Unavailable); } });
        });
        const client = SpiceDBClient.fromTransport(transport, { streamRetries: StreamRetries.Disabled, retryPolicy: { ...DEFAULT_RETRY_POLICY, initialBackoffMs: 1 } });
        await expect((async () => { for await (const _ of client.readRelationships({ filter: { resourceType: "document" }, consistency: full() })) { /* drain */ } })()).rejects.toBeInstanceOf(UnavailableError);
        expect(calls).toBe(1);
    });
    it("DefaultDeadline.Disabled sends no deadline; a per-call timeout still does", async () => {
        const seen: (number | undefined)[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(SchemaService, { readSchema(_req, ctx) { seen.push(ctx.timeoutMs()); return create(ReadSchemaResponseSchema, {}); } });
        });
        const client = SpiceDBClient.fromTransport(transport, { defaultDeadline: DefaultDeadline.Disabled });
        await client.readSchema();
        await client.readSchema({ timeoutMs: 5000 });
        await SpiceDBClient.fromTransport(transport).readSchema();
        expect(seen[0]).toBeUndefined();
        expect(seen[1]).toBeGreaterThan(0);
        expect(seen[2]).toBeGreaterThan(20000);
        expect(() => SpiceDBClient.fromTransport(transport, { defaultDeadline: DefaultDeadline.Disabled, defaultTimeoutMs: 5 })).toThrow(InvalidArgumentError);
    });
});

describe("N14 transport escape hatch", () => {
    it("forwards per-call headers and captures response headers and trailers", async () => {
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(SchemaService, { readSchema(_req, ctx) {
                ctx.responseHeader.set("x-echo", ctx.requestHeader.get("x-caller") ?? "missing");
                ctx.responseTrailer.set("x-trailer", "t");
                return create(ReadSchemaResponseSchema, {});
            } });
        });
        let header: Headers | undefined, trailer: Headers | undefined;
        await SpiceDBClient.fromTransport(transport).readSchema({ headers: { "x-caller": "zed" }, onHeader: h => { header = h; }, onTrailer: t => { trailer = t; } });
        expect(header?.get("x-echo")).toBe("zed");
        expect(trailer?.get("x-trailer")).toBe("t");
    });
});

describe("N8 paging: stable consistency and page size, cursors, read-at", () => {
    it("readRelationships repeats consistency and page size and enforces limit client-side", async () => {
        const requests: ProtoReadRequest[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async *readRelationships(req) {
                requests.push(req);
                const start = req.optionalCursor ? Number(req.optionalCursor.token) : 0;
                for (let i = start; i < Math.min(start + req.optionalLimit, 7); i++)
                    yield create(ReadRelationshipsResponseSchema, { readAt: create(ZedTokenSchema, { token: `rev-${requests.length}` }), relationship: wireRel(`d${i}`), afterResultCursor: create(CursorSchema, { token: String(i + 1) }) });
            } });
        });
        const client = SpiceDBClient.fromTransport(transport);
        const items = [];
        for await (const item of client.readRelationships({ filter: { resourceType: "document" }, consistency: atLeast(new ZedToken("t")), pageSize: 2, limit: 5 }))
            items.push(item);
        expect(items.map(i => i.resource.objectId)).toEqual(["d0", "d1", "d2", "d3", "d4"]);
        expect(items[4].afterResultCursor).toBe("5");
        expect(items[2].readAt.toString()).toBe("rev-2");
        expect(requests.map(r => r.optionalLimit)).toEqual([2, 2, 2]);
        expect(new Set(requests.map(r => JSON.stringify(r.consistency?.requirement))).size).toBe(1);
        expect(requests[0].consistency?.requirement.case).toBe("atLeastAsFresh");
    });
    it("lookupResources keeps one page size across pages (regression: the last page used to shrink)", async () => {
        const requests: ProtoLookupRequest[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async *lookupResources(req) {
                requests.push(req);
                const start = req.optionalCursor ? Number(req.optionalCursor.token) : 0;
                for (let i = start; i < start + req.optionalLimit; i++)
                    yield create(LookupResourcesResponseSchema, { resourceObjectId: `d${i}`, permissionship: LookupPermissionship.HAS_PERMISSION, afterResultCursor: create(CursorSchema, { token: String(i + 1) }) });
            } });
        });
        const ids: string[] = [];
        const cursors: (string | undefined)[] = [];
        for await (const r of SpiceDBClient.fromTransport(transport).lookupResources({ resourceType: "document", permission: "view", subject: { object: { objectType: "user", objectId: "a" } }, consistency: full(), pageSize: 2, limit: 5 })) {
            ids.push(r.resourceId);
            cursors.push(r.afterResultCursor);
        }
        expect(ids).toHaveLength(5);
        expect(cursors[4]).toBe("5");
        expect(requests.map(r => r.optionalLimit)).toEqual([2, 2, 2]);
    });
    it("exportRelationships yields each batch's cursor", async () => {
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async *exportBulkRelationships() {
                yield create(ExportBulkRelationshipsResponseSchema, { relationships: [wireRel("a"), wireRel("b")], afterResultCursor: create(CursorSchema, { token: "after-b" }) });
            } });
        });
        const items = [];
        for await (const r of SpiceDBClient.fromTransport(transport).exportRelationships({ consistency: full() })) items.push(r);
        expect(items.map(i => i.afterResultCursor)).toEqual(["after-b", "after-b"]);
    });
});

describe("N12 bulk item errors", () => {
    const transport = createRouterTransport(({ service }: ConnectRouter) => {
        service(PermissionsService, { checkBulkPermissions(req) {
            return create(CheckBulkPermissionsResponseSchema, { checkedAt: create(ZedTokenSchema, { token: "rev" }), pairs: req.items.map((item, i) => create(CheckBulkPermissionsPairSchema, { request: item,
                response: i === 1 ? { case: "error", value: { code: Code.NotFound, message: "no such type", details: [] } }
                    : { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: CheckPermissionResponse_Permissionship.HAS_PERMISSION }) } })) });
        } });
    });
    it("fails on the first item error by default", async () => {
        await expect(SpiceDBClient.fromTransport(transport).checkBulk([checkReq("a"), checkReq("b")])).rejects.toMatchObject({ inputIndex: 1, code: Code.NotFound });
    });
    it("returns every item with BulkItemErrors.Returned; errored items never grant", async () => {
        const results = await SpiceDBClient.fromTransport(transport).checkBulk([checkReq("a"), checkReq("b"), checkReq("c")], { itemErrors: BulkItemErrors.Returned });
        expect(results.map(r => r.hasPermission())).toEqual([true, false, true]);
        expect(results[1].error).toBeInstanceOf(NotFoundError);
        expect(results[1].error?.serverMessage).toBe("no such type");
        expect(results[1].error?.inputIndex).toBe(1);
        expect(results[1].permissionship).toBe("unspecified");
        expect(results[0].error).toBeUndefined();
    });
    it("rejects a granting result that carries an error", () => {
        expect(() => new CheckResult("hasPermission", [], new ZedToken("r"), undefined, undefined, new SpiceDBError("x"))).toThrow(InvalidArgumentError);
    });
});

describe("N7/N13 import", () => {
    it("sends the configured batch size", async () => {
        const sizes: number[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async importBulkRelationships(reqs) {
                for await (const req of reqs) sizes.push(req.relationships.length);
                return create(ImportBulkRelationshipsResponseSchema, { numLoaded: BigInt(sizes.reduce((a, b) => a + b, 0)) });
            } });
        });
        const rels = Array.from({ length: 5 }, (_, i) => docViewer(`d${i}`, "u"));
        expect(await SpiceDBClient.fromTransport(transport).importRelationships(rels, { batchSize: 2 })).toBe(5n);
        expect(sizes).toEqual([2, 2, 1]);
        await expect(SpiceDBClient.fromTransport(transport).importRelationships(rels, { batchSize: 0 })).rejects.toBeInstanceOf(InvalidArgumentError);
    });
    it("reports the server's status when the server aborts mid-stream", async () => {
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async importBulkRelationships(reqs) {
                for await (const _ of reqs) throw new ConnectError("already existed", Code.AlreadyExists);
                return create(ImportBulkRelationshipsResponseSchema, {});
            } });
        });
        const rels = Array.from({ length: 50 }, (_, i) => docViewer(`d${i}`, "u"));
        const error = await SpiceDBClient.fromTransport(transport).importRelationships(rels, { batchSize: 1 }).catch(e => e);
        expect(error).toBeInstanceOf(AlreadyExistsError);
        expect(error.serverMessage).toBe("already existed");
    });
});

describe("N16 deletion modes and progress", () => {
    it("sends all-or-nothing deletes with an optional limit", async () => {
        const requests: ProtoDeleteRequest[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { deleteRelationships(req) { requests.push(req); return create(DeleteRelationshipsResponseSchema, { deletionProgress: DeleteRelationshipsResponse_DeletionProgress.COMPLETE }); } });
        });
        const client = SpiceDBClient.fromTransport(transport);
        await client.deleteRelationships({ resourceType: "document" }, { partial: DeletePartial.Disallowed });
        await client.deleteRelationships({ resourceType: "document" }, { partial: DeletePartial.Disallowed, limit: 10 });
        await client.deleteRelationships({ resourceType: "document" }, { limit: 3 });
        expect(requests.map(r => [r.optionalAllowPartialDeletions, r.optionalLimit])).toEqual([[false, 0], [false, 10], [true, 3]]);
        await expect(client.deleteRelationships({ resourceType: "document" }, {} as never)).rejects.toBeInstanceOf(InvalidArgumentError);
    });
    it("reports cumulative progress after every acknowledged batch", async () => {
        let batch = 0;
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { deleteRelationships() {
                batch++;
                return create(DeleteRelationshipsResponseSchema, { relationshipsDeletedCount: 2n, deletedAt: create(ZedTokenSchema, { token: `r${batch}` }), deletionProgress: batch < 3 ? DeleteRelationshipsResponse_DeletionProgress.PARTIAL : DeleteRelationshipsResponse_DeletionProgress.COMPLETE, afterResultCursor: batch < 3 ? create(CursorSchema, { token: `c${batch}` }) : undefined });
            } });
        });
        const onProgress = vi.fn();
        const result = await SpiceDBClient.fromTransport(transport).deleteAllRelationshipsNonAtomic({ resourceType: "document" }, { pageSize: 2, onProgress });
        expect(result.deletedCount).toBe(6n);
        expect(onProgress.mock.calls.map(([p]) => [p.deletedCount, p.revision.toString(), p.cursor])).toEqual([[2n, "r1", "c1"], [4n, "r2", "c2"], [6n, "r3", undefined]]);
    });
});

describe("N11 protoconv", () => {
    it("round-trips relationships, filters, preconditions, updates, consistency and results", () => {
        const rel = relationshipFromTuple("document:a#viewer@team:eng#member[c:{\"k\":[1,null]}][expiration:2030-01-02T03:04:05Z]");
        expect(protoconv.relationshipFromProto(protoconv.relationshipToProto(rel))).toEqual(rel);
        const filter: RelationshipFilter = { resourceType: "document", resourceIdPrefix: "a", relation: "viewer", subject: { subjectType: "user", relation: { kind: SubjectRelationKind.Direct } } };
        expect(protoconv.filterFromProto(protoconv.filterToProto(filter))).toEqual(filter);
        const named: RelationshipFilter = { resourceType: "document", subject: { subjectType: "team", subjectId: "eng", relation: { kind: SubjectRelationKind.Named, relation: "member" } } };
        expect(protoconv.filterFromProto(protoconv.filterToProto(named))).toEqual(named);
        expect(protoconv.preconditionFromProto(protoconv.preconditionToProto({ operation: "mustNotMatch", filter }))).toEqual({ operation: "mustNotMatch", filter });
        expect(protoconv.updateFromProto(protoconv.updateToProto({ operation: "touch", relationship: rel }))).toEqual({ operation: "touch", relationship: rel });
        expect(protoconv.consistencyFromProto(protoconv.consistencyToProto(atLeast(new ZedToken("t")))).toString()).toBe("atLeast(t)");
        expect(protoconv.zedTokenFromProto(protoconv.zedTokenToProto(new ZedToken("z"))).toString()).toBe("z");
        const response = create(CheckPermissionResponseSchema, { checkedAt: create(ZedTokenSchema, { token: "r" }), permissionship: CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION, partialCaveatInfo: { missingRequiredContext: ["k"] },
            debugTrace: { schemaUsed: "s", check: { permission: "view", permissionType: 2, result: 3, duration: { seconds: 1n, nanos: 5 }, resolution: { case: "subProblems", value: { traces: [{ permission: "viewer", resolution: { case: "wasCachedResult", value: false } }] } }, traceOperationId: "op", source: "src" } } });
        const native = protoconv.checkResultFromProto(response);
        expect(native.debugTrace?.check?.durationNs).toBe(1_000_000_005n);
        expect(protoconv.checkResultFromProto(protoconv.checkResultToProto(native))).toEqual(native);
        const item = { ...docViewer("a", "b"), afterResultCursor: "c", readAt: new ZedToken("r") };
        expect(protoconv.readRelationshipFromProto(protoconv.readRelationshipToProto(item))).toEqual(item);
        expect(protoconv.writeResultToProto({ revision: new ZedToken("w") }).writtenAt?.token).toBe("w");
        const lookup = { resourceId: "a", permissionship: "conditionalPermission" as const, partialCaveat: { missingRequiredContext: ["k"] }, lookedUpAt: new ZedToken("l"), afterResultCursor: "c" };
        expect(protoconv.lookupResourceFromProto(protoconv.lookupResourceToProto(lookup))).toEqual(lookup);
        const expand = { revision: new ZedToken("e"), treeRoot: { expandedObject: { objectType: "document", objectId: "a" }, expandedRelation: "view", node: { kind: "intermediate" as const, operation: "union" as const, children: [{ expandedObject: { objectType: "document", objectId: "a" }, expandedRelation: "viewer", node: { kind: "leaf" as const, subjects: [{ object: { objectType: "user", objectId: "b" } }] } }] } } };
        expect(protoconv.expandResultFromProto(protoconv.expandResultToProto(expand))).toEqual(expand);
    });
});

describe("InMemorySpiceDB test double", () => {
    type Authz = Pick<SpiceDBClient, "write" | "readRelationships" | "deleteRelationships" | "deleteAllRelationshipsNonAtomic" | "check" | "checkBulk" | "close">;
    it("matches the client's surface", () => {
        const both: Authz[] = [new InMemorySpiceDB(), SpiceDBClient.fromTransport(createRouterTransport(() => {}))];
        expect(both).toHaveLength(2);
    });
    it("applies preconditions, create conflicts, filters and deletion bounds", async () => {
        const db = new InMemorySpiceDB([docViewer("a", "alice")]);
        await expect(db.write(new Transaction().create(docViewer("a", "alice")))).rejects.toBeInstanceOf(AlreadyExistsError);
        await expect(db.write(new Transaction().mustMatch({ resourceType: "document", resourceId: "zzz" }).touch(docViewer("b", "bob")))).rejects.toMatchObject({ code: Code.FailedPrecondition });
        expect(db.relationships).toHaveLength(1);
        const { revision } = await db.write(new Transaction().mustNotMatch({ resourceType: "document", resourceId: "b" }).touch(docViewer("b", "bob")).touch(docViewer("c", "carol")));
        expect(revision).toBeInstanceOf(ZedToken);
        const read = [];
        for await (const r of db.readRelationships({ filter: { resourceType: "document", subject: { subjectType: "user", subjectId: "bob" } }, consistency: full() })) read.push(r);
        expect(read.map(r => r.resource.objectId)).toEqual(["b"]);
        await expect(db.deleteRelationships({ resourceType: "document" }, { partial: DeletePartial.Disallowed, limit: 2 })).rejects.toBeInstanceOf(InvalidArgumentError);
        expect((await db.deleteRelationships({ resourceType: "document" }, { limit: 2 })).progress).toBe("partial");
        expect((await db.deleteAllRelationshipsNonAtomic({ resourceType: "document" })).deletedCount).toBe(1n);
        expect(db.relationships).toHaveLength(0);
    });
    it("returns registered check results and errors", async () => {
        const db = new InMemorySpiceDB();
        db.setCheckResult(checkReq("a"), "hasPermission");
        db.setCheckResult(checkReq("b"), SpiceDBError.fromCode(Code.NotFound, "gone"));
        expect((await db.check(checkReq("a"))).hasPermission()).toBe(true);
        expect((await db.check(checkReq("z"))).permissionship).toBe("noPermission");
        await expect(db.check(checkReq("b"))).rejects.toBeInstanceOf(NotFoundError);
        const bulk = await db.checkBulk([checkReq("a"), checkReq("b")], { itemErrors: BulkItemErrors.Returned });
        expect(bulk[1].error?.inputIndex).toBe(1);
    });
});
