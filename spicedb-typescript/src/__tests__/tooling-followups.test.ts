// Per-call retry override, empty writes, single-request paging, wrapper error
// status, whole-response protocol conversions and the fuller test double (root
// DESIGN.md: "Retry only when replay is safe", "Expose paging state on paged
// reads", "Expose native errors...", "Keep protocol access secondary",
// "Make native values observable and testable").
import { describe, it, expect } from "vitest";
import { ConnectError, Code, createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { create, equals } from "@bufbuild/protobuf";
import {
    PermissionsService, SchemaService, WatchService, ExperimentalService, ReadSchemaResponseSchema, WriteSchemaResponseSchema, WriteRelationshipsResponseSchema,
    ReadRelationshipsResponseSchema, LookupResourcesResponseSchema, LookupSubjectsResponseSchema, CheckBulkPermissionsResponseSchema, CheckBulkPermissionsPairSchema,
    CheckBulkPermissionsResponseItemSchema, CheckPermissionResponse_Permissionship, DeleteRelationshipsResponseSchema, DeleteRelationshipsResponse_DeletionProgress,
    LookupPermissionship, ZedTokenSchema, CursorSchema, type ReadRelationshipsRequest as ProtoReadRequest, type LookupResourcesRequest as ProtoLookupRequest,
    type WriteRelationshipsRequest as ProtoWriteRequest,
} from "@spicedb/proto";
import {
    SpiceDBClient, Transaction, relationship, full, minLatency, ZedToken, SpiceDBError, InvalidArgumentError, UnavailableError, AlreadyExistsError, NotFoundError,
    FailedPreconditionError, errorCode, toConnectError, Retries, BulkItemErrors, Paging, DEFAULT_RETRY_POLICY, InMemorySpiceDB, protoconv, type RetryPolicy,
} from "../index.js";

const docViewer = (doc: string, user: string) => relationship(`document:${doc}`, "viewer", `user:${user}`);
const wireRel = (doc: string) => ({ resource: { objectType: "document", objectId: doc }, relation: "viewer", subject: { object: { objectType: "user", objectId: "u" }, optionalRelation: "" } });
const retryOnce: RetryPolicy = { maxRetries: 1, retryableCodes: [Code.Unavailable], initialBackoffMs: 1 };
const noRetries: RetryPolicy = { ...DEFAULT_RETRY_POLICY, maxRetries: 0 };
const drain = async <T>(it: AsyncIterable<T>) => { const out: T[] = []; for await (const x of it) out.push(x); return out; };
const rejection = (p: Promise<unknown>) => p.then(() => { throw new Error("expected a rejection"); }, (e: unknown) => e as SpiceDBError);
const thrown = (f: () => unknown) => { try { f(); } catch (e) { return e as SpiceDBError; } throw new Error("expected a throw"); };

describe("per-call retry policy", () => {
    function flakyServer() {
        const calls = { write: 0, schema: 0, delete: 0, import: 0, counter: 0, read: 0, watch: 0, readSchema: 0 };
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, {
                writeRelationships() { if (calls.write++ === 0) throw new ConnectError("transient", Code.Unavailable); return create(WriteRelationshipsResponseSchema, { writtenAt: create(ZedTokenSchema, { token: "w" }) }); },
                deleteRelationships() { calls.delete++; return create(DeleteRelationshipsResponseSchema, { deletionProgress: DeleteRelationshipsResponse_DeletionProgress.COMPLETE }); },
                async importBulkRelationships() { calls.import++; throw new ConnectError("no", Code.Internal); },
                async *readRelationships() { calls.read++; throw new ConnectError("down", Code.Unavailable); },
            });
            service(SchemaService, {
                writeSchema() { if (calls.schema++ === 0) throw new ConnectError("transient", Code.Unavailable); return create(WriteSchemaResponseSchema, { writtenAt: create(ZedTokenSchema, { token: "s" }) }); },
                readSchema() { if (calls.readSchema++ === 0) throw new ConnectError("transient", Code.Unavailable); return create(ReadSchemaResponseSchema, { schemaText: "ok" }); },
            });
            service(ExperimentalService, { experimentalRegisterRelationshipCounter() { calls.counter++; return {}; }, experimentalUnregisterRelationshipCounter() { calls.counter++; return {}; } });
            service(WatchService, { async *watch() { calls.watch++; throw new ConnectError("down", Code.Unavailable); } });
        });
        return { transport, calls };
    }

    it("never retries a mutation by default", async () => {
        const { transport, calls } = flakyServer();
        await expect(SpiceDBClient.fromTransport(transport).write(new Transaction().touch(docViewer("a", "b")))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.write).toBe(1);
    });
    it("lets a touch-only write and a schema write opt in for one call", async () => {
        const { transport, calls } = flakyServer();
        const client = SpiceDBClient.fromTransport(transport, { retries: Retries.Disabled });
        expect((await client.write(new Transaction().touch(docViewer("a", "b")), { retryPolicy: retryOnce })).revision.toString()).toBe("w");
        expect(calls.write).toBe(2);
        expect((await client.writeSchema("definition user {}", { retryPolicy: retryOnce })).toString()).toBe("s");
        expect(calls.schema).toBe(2);
        // The per-call policy also overrides Retries.Disabled for reads.
        expect((await client.readSchema({ retryPolicy: retryOnce })).schema).toBe("ok");
        expect(calls.readSchema).toBe(2);
    });
    it("rejects the opt-in on every other mutation before any request", async () => {
        const { transport, calls } = flakyServer();
        const client = SpiceDBClient.fromTransport(transport);
        const o = { retryPolicy: retryOnce };
        const attempts: Promise<unknown>[] = [
            client.write(new Transaction().create(docViewer("a", "b")), o),
            client.write(new Transaction().delete(docViewer("a", "b")), o),
            client.write(new Transaction().mustMatch({ resourceType: "document" }).touch(docViewer("a", "b")), o),
            client.deleteRelationships({ resourceType: "document" }, { limit: 1, ...o }),
            client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, o),
            client.importRelationships([docViewer("a", "b")], o),
            client.experimentalRegisterRelationshipCounter("c", { resourceType: "document" }, o),
            client.experimentalUnregisterRelationshipCounter("c", o),
        ];
        for (const attempt of attempts)
            await expect(attempt).rejects.toBeInstanceOf(InvalidArgumentError);
        expect(calls).toMatchObject({ write: 0, delete: 0, import: 0, counter: 0 });
        // A policy without retries is not an opt-in, so any mutation accepts it.
        await client.deleteRelationships({ resourceType: "document" }, { limit: 1, retryPolicy: noRetries });
        expect(calls.delete).toBe(1);
        await expect(client.readSchema({ retryPolicy: { ...retryOnce, initialBackoffMs: 0 } })).rejects.toBeInstanceOf(InvalidArgumentError);
    });
    it("governs stream establishment for one call", async () => {
        const { transport, calls } = flakyServer();
        const client = SpiceDBClient.fromTransport(transport, { retryPolicy: { ...DEFAULT_RETRY_POLICY, initialBackoffMs: 1 } });
        // One read stream is exempted while the client retries everything else.
        await expect(drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }, { retryPolicy: noRetries }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.read).toBe(1);
        await expect(drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.read).toBe(1 + 4);
        // Watch is never reopened by default; a per-call policy opts one in.
        await expect(drain(client.watch())).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.watch).toBe(1);
        await expect(drain(client.watch(undefined, { retryPolicy: retryOnce }))).rejects.toBeInstanceOf(UnavailableError);
        expect(calls.watch).toBe(1 + 2);
    });
});

describe("empty writes", () => {
    it("are sent to the server, which checks preconditions and returns the revision", async () => {
        const requests: ProtoWriteRequest[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { writeRelationships(req) { requests.push(req); return create(WriteRelationshipsResponseSchema, { writtenAt: create(ZedTokenSchema, { token: "current" }) }); } });
        });
        const result = await SpiceDBClient.fromTransport(transport).write(new Transaction().mustMatch({ resourceType: "document" }));
        expect(result.revision.toString()).toBe("current");
        expect(requests).toHaveLength(1);
        expect(requests[0].updates).toHaveLength(0);
        expect(requests[0].optionalPreconditions).toHaveLength(1);

        const db = new InMemorySpiceDB([docViewer("a", "alice")]);
        expect((await db.write(new Transaction())).revision).toBeInstanceOf(ZedToken);
        await expect(db.write(new Transaction().mustNotMatch({ resourceType: "document" }))).rejects.toBeInstanceOf(FailedPreconditionError);
    });
});

describe("single-request paging", () => {
    it("sends exactly one request with the total limit, or unbounded", async () => {
        const reads: ProtoReadRequest[] = [];
        const lookups: ProtoLookupRequest[] = [];
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, {
                async *readRelationships(req) {
                    reads.push(req);
                    for (let i = 0; i < (req.optionalLimit || 3); i++)
                        yield create(ReadRelationshipsResponseSchema, { relationship: wireRel(`d${i}`), afterResultCursor: create(CursorSchema, { token: `c${i}` }) });
                },
                async *lookupResources(req) {
                    lookups.push(req);
                    for (let i = 0; i < (req.optionalLimit || 3); i++)
                        yield create(LookupResourcesResponseSchema, { resourceObjectId: `d${i}`, permissionship: LookupPermissionship.HAS_PERMISSION, afterResultCursor: create(CursorSchema, { token: `c${i}` }) });
                },
            });
        });
        const client = SpiceDBClient.fromTransport(transport);
        const read = await drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full(), paging: Paging.SingleRequest, pageSize: 1 }));
        expect(read.map(r => r.afterResultCursor)).toEqual(["c0", "c1", "c2"]);
        expect(reads.map(r => r.optionalLimit)).toEqual([0]);
        // A full page with a limit fetches no follow-up page.
        await drain(client.readRelationships({ filter: { resourceType: "document" }, consistency: full(), paging: Paging.SingleRequest, limit: 2 }));
        expect(reads.map(r => r.optionalLimit)).toEqual([0, 2]);

        const subject = { object: { objectType: "user", objectId: "a" } };
        const found = await drain(client.lookupResources({ resourceType: "document", permission: "view", subject, consistency: full(), paging: Paging.SingleRequest }));
        expect(found.map(r => r.afterResultCursor)).toEqual(["c0", "c1", "c2"]);
        await drain(client.lookupResources({ resourceType: "document", permission: "view", subject, consistency: full(), paging: Paging.SingleRequest, limit: 2 }));
        expect(lookups.map(r => r.optionalLimit)).toEqual([0, 2]);
        expect(() => client.lookupResources({ resourceType: "document", permission: "view", subject, consistency: full(), paging: "sometimes" as Paging })).toThrow(InvalidArgumentError);
    });
});

describe("wrapper errors report the server's status once", () => {
    it("bulk item and delete-progress errors convert to the inner status", async () => {
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, {
                checkBulkPermissions(req) {
                    return create(CheckBulkPermissionsResponseSchema, { pairs: req.items.map(item => create(CheckBulkPermissionsPairSchema, { request: item, response: { case: "error", value: { code: Code.FailedPrecondition, message: "no such permission", details: [] } } })) });
                },
                deleteRelationships(req) {
                    if (req.optionalCursor) throw new ConnectError("gone", Code.Unavailable);
                    return create(DeleteRelationshipsResponseSchema, { relationshipsDeletedCount: 1n, deletionProgress: DeleteRelationshipsResponse_DeletionProgress.PARTIAL, afterResultCursor: create(CursorSchema, { token: "c" }) });
                },
            });
        });
        const client = SpiceDBClient.fromTransport(transport, { retries: Retries.Disabled });
        const check = { resource: { objectType: "document", objectId: "a" }, permission: "view", subject: { object: { objectType: "user", objectId: "u" } }, consistency: full() };
        const item = await client.checkBulk([check], { itemErrors: BulkItemErrors.Returned }).then(r => r[0].error!);
        expect(item.message).toBe("check item 0: [failed_precondition] no such permission");
        expect(toConnectError(item).code).toBe(Code.FailedPrecondition);
        expect(toConnectError(item).rawMessage).toBe("no such permission");
        expect(errorCode(item)).toBe(Code.FailedPrecondition);

        const progress = await rejection(client.deleteAllRelationshipsNonAtomic({ resourceType: "document" }));
        expect(progress.deleteProgress?.deletedCount).toBe(1n);
        expect(toConnectError(progress).rawMessage).toBe("gone");
        expect(errorCode(progress)).toBe(Code.Unavailable);
    });
});

describe("protocol conversions reproduce the server's responses", () => {
    it("rebuilds a whole bulk-check response with per-item errors", async () => {
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { checkBulkPermissions(req) {
                return create(CheckBulkPermissionsResponseSchema, { checkedAt: create(ZedTokenSchema, { token: "bulk-rev" }), pairs: req.items.map(item => create(CheckBulkPermissionsPairSchema, { request: item,
                    response: item.resource?.objectId === "broken" ? { case: "error", value: { code: Code.FailedPrecondition, message: "permission view not found", details: [] } }
                        : { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION, partialCaveatInfo: { missingRequiredContext: ["cidr"] } }) } })) });
            } });
        });
        const base = { permission: "view", subject: { object: { objectType: "user", objectId: "u" } }, consistency: full() };
        const requests = [
            { ...base, resource: { objectType: "document", objectId: "a" } },
            { ...base, resource: { objectType: "document", objectId: "broken" } },
            { ...base, resource: { objectType: "document", objectId: "c" }, context: { cidr: "10.0.0.0/8" } },
        ];
        const defaults = { region: "us" };
        const results = await SpiceDBClient.fromTransport(transport).checkBulk(requests, { itemErrors: BulkItemErrors.Returned, context: defaults });
        const got = protoconv.checkBulkToProto(requests, results, defaults);
        const want = create(CheckBulkPermissionsResponseSchema, { checkedAt: create(ZedTokenSchema, { token: "bulk-rev" }), pairs: requests.map(r => create(CheckBulkPermissionsPairSchema, {
            request: protoconv.checkBulkRequestItemToProto(r, defaults),
            response: r.resource.objectId === "broken" ? { case: "error", value: { code: Code.FailedPrecondition, message: "permission view not found", details: [] } }
                : { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: CheckPermissionResponse_Permissionship.CONDITIONAL_PERMISSION, partialCaveatInfo: { missingRequiredContext: ["cidr"] } }) },
        })) });
        expect(equals(CheckBulkPermissionsResponseSchema, got, want)).toBe(true);
        expect(() => protoconv.checkBulkToProto(requests.slice(0, 1), results)).toThrow(InvalidArgumentError);
    });
    it("fills the deprecated lookup-subjects fields SpiceDB still sends", async () => {
        // As SpiceDB sends it: the deprecated top-level fields mirror subject.
        const wire = create(LookupSubjectsResponseSchema, {
            lookedUpAt: { token: "rev3" },
            subject: { subjectObjectId: "*", permissionship: LookupPermissionship.HAS_PERMISSION },
            subjectObjectId: "*", permissionship: LookupPermissionship.HAS_PERMISSION,
            excludedSubjects: [{ subjectObjectId: "bob", permissionship: LookupPermissionship.CONDITIONAL_PERMISSION, partialCaveatInfo: { missingRequiredContext: ["k"] } }],
            excludedSubjectIds: ["bob"],
        });
        const transport = createRouterTransport(({ service }: ConnectRouter) => {
            service(PermissionsService, { async *lookupSubjects() { yield wire; } });
        });
        const [subject] = await drain(SpiceDBClient.fromTransport(transport).lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "view", subjectType: "user", consistency: full() }));
        expect(equals(LookupSubjectsResponseSchema, protoconv.lookupSubjectToProto(subject), wire)).toBe(true);
    });
});

describe("InMemorySpiceDB matches the real client", () => {
    it("shapes errors like the client's server errors and local validation", async () => {
        const db = new InMemorySpiceDB([docViewer("a", "alice")]);
        const error = await rejection(db.write(new Transaction().mustMatch({ resourceType: "document", resourceId: "zzz" })));
        expect(error).toBeInstanceOf(FailedPreconditionError);
        expect(error.message).toMatch(/^\[failed_precondition\] unable to satisfy write precondition `MUST_MATCH\(document:zzz/);
        expect(error.message).toBe(`[failed_precondition] ${error.serverMessage}`);
        expect(error.reason).toBe("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
        expect(toConnectError(error).rawMessage).toBe(error.serverMessage);
        // Local validation throws at the call, as the client's does.
        const local = thrown(() => db.readRelationships({ filter: { resourceType: "" }, consistency: full() }));
        expect(thrown(() => SpiceDBClient.fromTransport(createRouterTransport(() => {})).readRelationships({ filter: { resourceType: "" }, consistency: full() })).message).toBe(local.message);
        expect(local).toBeInstanceOf(InvalidArgumentError);
        expect(local.serverMessage).toBeUndefined();
        await expect(db.write(new Transaction().create(docViewer("b", "b")), { retryPolicy: retryOnce })).rejects.toBeInstanceOf(InvalidArgumentError);
        await expect(db.importRelationships([], { retryPolicy: retryOnce })).rejects.toBeInstanceOf(InvalidArgumentError);
    });
    it("imports, exports, pages and serves the schema", async () => {
        const db = new InMemorySpiceDB();
        type Tooling = Pick<SpiceDBClient, "importRelationships" | "exportRelationships" | "readSchema" | "writeSchema" | "reflectSchema">;
        const both: Tooling[] = [db, SpiceDBClient.fromTransport(createRouterTransport(() => {}))];
        expect(both).toHaveLength(2);

        expect(await db.importRelationships([docViewer("a", "x"), docViewer("b", "x"), docViewer("c", "x"), relationship("folder:f", "viewer", "user:x")])).toBe(4n);
        const duplicate = await rejection(db.importRelationships((async function* () { yield docViewer("z", "x"); yield docViewer("a", "x"); })()));
        expect(duplicate).toBeInstanceOf(AlreadyExistsError);
        expect(duplicate.serverMessage).toMatch(/duplicate relationship/);
        expect(db.relationships).toHaveLength(4);

        const first = await drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), limit: 2 }));
        expect(first.map(r => r.resource.objectId)).toEqual(["a", "b"]);
        const rest = await drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), cursor: first[1].afterResultCursor }));
        expect(rest.map(r => r.resource.objectId)).toEqual(["c"]);
        await expect(drain(db.readRelationships({ filter: { resourceType: "document" }, consistency: full(), cursor: "not-a-cursor" }))).rejects.toMatchObject({ code: Code.InvalidArgument, serverMessage: "invalid cursor" });

        const exported = await drain(db.exportRelationships({ consistency: full(), filter: { resourceType: "folder" } }));
        expect(exported.map(r => r.resource.objectId)).toEqual(["f"]);
        expect((await drain(db.exportRelationships({ consistency: full(), cursor: "3" }))).map(r => r.resource.objectId)).toEqual(["f"]);

        await expect(db.readSchema()).rejects.toBeInstanceOf(NotFoundError);
        await db.writeSchema("definition user {}", { retryPolicy: retryOnce });
        expect((await db.readSchema()).schema).toBe("definition user {}");
        await expect(db.reflectSchema({ consistency: minLatency() })).rejects.toBeInstanceOf(NotFoundError);
        db.setReflectSchemaResult({ definitions: [{ name: "user", comment: "", relations: [], permissions: [] }], caveats: [] });
        const reflected = await db.reflectSchema({ consistency: minLatency() });
        expect(reflected.definitions[0].name).toBe("user");
        expect(reflected.revision).toBeInstanceOf(ZedToken);
    });
    it("reports registered response metadata to onHeader and onTrailer", async () => {
        const db = new InMemorySpiceDB();
        db.setResponseMetadata({ "io.spicedb.respmeta.version": "v1.99" }, { t: "1" });
        let header: Headers | undefined, trailer: Headers | undefined;
        await db.writeSchema("definition user {}");
        await db.readSchema({ onHeader: h => { header = h; }, onTrailer: t => { trailer = t; } });
        expect(header?.get("io.spicedb.respmeta.version")).toBe("v1.99");
        expect(trailer?.get("t")).toBe("1");
    });
});
