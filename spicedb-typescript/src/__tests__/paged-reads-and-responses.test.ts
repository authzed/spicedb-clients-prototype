// Per-page default deadlines, resumable paged reads, malformed responses,
// local cancellation, token equality, the double's schema diff and the
// startup readiness check (root DESIGN.md: "Finite paged reads ... apply the
// client default deadline to each page request", "A response the client
// cannot interpret ... fails with INTERNAL", "Local cancellation surfaces as
// the client's cancelled error").
import { describe, it, expect } from "vitest";
import { isDeepStrictEqual } from "node:util";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import {
    PermissionsService, WatchService, ExperimentalService, SchemaService,
    LookupResourcesResponseSchema, ReadRelationshipsResponseSchema, ExportBulkRelationshipsResponseSchema, LookupSubjectsResponseSchema,
    ResolvedSubjectSchema, WatchResponseSchema, RelationshipUpdateSchema, RelationshipUpdate_Operation, ExpandPermissionTreeResponseSchema,
    ExperimentalCountRelationshipsResponseSchema, ReadSchemaResponseSchema, CheckBulkPermissionsResponseSchema, CheckBulkPermissionsPairSchema,
    LookupPermissionship, RelationshipSchema, ObjectReferenceSchema, SubjectReferenceSchema,
} from "@spicedb/proto";
import {
    SpiceDBClient, InMemorySpiceDB, createSpiceDBClient, full, ZedToken, Retries, DefaultDeadline, TransportSecurity,
    SpiceDBError, CancelledError, DeadlineExceededError, UnavailableError, UnauthenticatedError, InvalidArgumentError, errorCode,
    type SpiceDBClientInterface, type SpiceDBClientBehaviorOptions, type SchemaDiffKind, type SchemaDiff,
} from "../index.js";

const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));
const STALL_MS = 3000;
const subject = { object: { objectType: "user", objectId: "alice" } };
const lookupRequest = { resourceType: "document", permission: "view", subject, consistency: full() };
const noRetries: SpiceDBClientBehaviorOptions = { retries: Retries.Disabled };
function clientFor(routes: (router: ConnectRouter) => void, options: SpiceDBClientBehaviorOptions = noRetries): SpiceDBClient {
    return SpiceDBClient.fromTransport(createRouterTransport(routes), options);
}
async function failure(iterable: AsyncIterable<unknown>, onItem?: () => Promise<void>): Promise<{ error: unknown; items: unknown[] }> {
    const items: unknown[] = [];
    try {
        for await (const item of iterable) {
            items.push(item);
            await onItem?.();
        }
    }
    catch (error) {
        return { error, items };
    }
    throw new Error("expected the read to fail");
}
const rel = (id: string) => create(RelationshipSchema, {
    resource: create(ObjectReferenceSchema, { objectType: "document", objectId: id }), relation: "viewer",
    subject: create(SubjectReferenceSchema, { object: create(ObjectReferenceSchema, { objectType: "user", objectId: "alice" }) }),
});
/** Pages of `pageSize` lookup results; the page at `stallPage` (1-based) stalls. */
function lookupPages(pageSize: number, total: number, stallPage?: number, pageDelayMs = 0) {
    return (router: ConnectRouter) => router.service(PermissionsService, {
        async *lookupResources(req) {
            const start = Number(req.optionalCursor?.token ?? 0);
            if (stallPage !== undefined && start / pageSize + 1 === stallPage)
                await sleep(STALL_MS);
            await sleep(pageDelayMs);
            for (let i = start; i < Math.min(total, start + req.optionalLimit); i++)
                yield create(LookupResourcesResponseSchema, { resourceObjectId: `r${i}`, permissionship: LookupPermissionship.HAS_PERMISSION, afterResultCursor: { token: String(i + 1) } });
        },
    });
}

describe("per-page default deadline on finite paged reads", () => {
    it("fails a lookup whose server stalls on page 3 after the per-page default, with no caller option", async () => {
        const client = clientFor(lookupPages(2, 10, 3), { defaultTimeoutMs: 200 });
        const start = Date.now();
        const { error, items } = await failure(client.lookupResources({ ...lookupRequest, pageSize: 2 }));
        expect(Date.now() - start).toBeLessThan(STALL_MS);
        expect(error).toBeInstanceOf(DeadlineExceededError);
        expect((error as Error).message).toMatch(/Page deadline exceeded/);
        expect(items).toHaveLength(4);
        // The read can resume after the last delivered item.
        expect((error as SpiceDBError).resumeCursor).toBe("4");
        client.close();
    });
    it("gives each page its own budget, so a read longer than the default completes", async () => {
        const client = clientFor(lookupPages(2, 8, undefined, 80), { defaultTimeoutMs: 150 });
        const ids: string[] = [];
        for await (const item of client.lookupResources({ ...lookupRequest, pageSize: 2 }))
            ids.push(item.resourceId);
        expect(ids).toHaveLength(8);
        client.close();
    });
    it("does not count the caller's loop body against the page budget", async () => {
        const client = clientFor(lookupPages(10, 4), { defaultTimeoutMs: 100 });
        const ids: string[] = [];
        for await (const item of client.lookupResources({ ...lookupRequest, pageSize: 10 })) {
            ids.push(item.resourceId);
            await sleep(60);
        }
        expect(ids).toEqual(["r0", "r1", "r2", "r3"]);
        client.close();
    });
    it("lets an earlier caller timeoutMs win over the per-page default", async () => {
        const client = clientFor(lookupPages(2, 10, 1), { defaultTimeoutMs: 2000 });
        const start = Date.now();
        const { error } = await failure(client.lookupResources({ ...lookupRequest, pageSize: 2 }, { timeoutMs: 100 }));
        expect(error).toBeInstanceOf(DeadlineExceededError);
        expect((error as Error).message).not.toMatch(/Page deadline/);
        expect(Date.now() - start).toBeLessThan(1000);
        client.close();
    });
    it("still bounds the whole read by the caller's total timeoutMs", async () => {
        const client = clientFor(lookupPages(2, 100, undefined, 60), { defaultTimeoutMs: 1000 });
        const { error, items } = await failure(client.lookupResources({ ...lookupRequest, pageSize: 2 }, { timeoutMs: 250 }));
        expect(error).toBeInstanceOf(DeadlineExceededError);
        expect(items.length).toBeLessThan(100);
        client.close();
    });
    it("applies to reads, subject lookups and export batches", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *readRelationships() { yield create(ReadRelationshipsResponseSchema, { relationship: rel("a"), afterResultCursor: { token: "1" } }); await sleep(STALL_MS); },
            async *lookupSubjects() { await sleep(STALL_MS); yield create(LookupSubjectsResponseSchema, {}); },
            async *exportBulkRelationships() {
                yield create(ExportBulkRelationshipsResponseSchema, { relationships: [rel("a")], afterResultCursor: { token: "b1" } });
                await sleep(STALL_MS);
            },
        }), { defaultTimeoutMs: 150 });
        const read = await failure(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }));
        expect(read.error).toBeInstanceOf(DeadlineExceededError);
        expect((read.error as SpiceDBError).resumeCursor).toBe("1");
        const subjects = await failure(client.lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "view", subjectType: "user", consistency: full() }));
        expect(subjects.error).toBeInstanceOf(DeadlineExceededError);
        const exported = await failure(client.exportRelationships({ consistency: full() }));
        expect(exported.error).toBeInstanceOf(DeadlineExceededError);
        expect((exported.error as SpiceDBError).resumeCursor).toBe("b1");
        client.close();
    });
    it("gives every export batch a fresh budget", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *exportBulkRelationships() {
                for (let i = 0; i < 4; i++) {
                    await sleep(80);
                    yield create(ExportBulkRelationshipsResponseSchema, { relationships: [rel(`d${i}`)], afterResultCursor: { token: `b${i}` } });
                }
            },
        }), { defaultTimeoutMs: 150 });
        let count = 0;
        for await (const _ of client.exportRelationships({ consistency: full() }))
            count++;
        expect(count).toBe(4);
        client.close();
    });
    it("is off under DefaultDeadline.Disabled", async () => {
        const client = clientFor(lookupPages(2, 2, undefined, 300), { defaultDeadline: DefaultDeadline.Disabled });
        let count = 0;
        for await (const _ of client.lookupResources({ ...lookupRequest, pageSize: 2 }))
            count++;
        expect(count).toBe(2);
        client.close();
    });
});

describe("resuming a failed paged read", () => {
    it("reports the cursor after the last delivered item, and resuming from it continues the read", async () => {
        let failOnce = true;
        const client = clientFor(router => router.service(PermissionsService, {
            async *readRelationships(req) {
                const start = Number(req.optionalCursor?.token ?? 0);
                if (start === 2 && failOnce) {
                    failOnce = false;
                    throw new ConnectError("down", Code.Unavailable);
                }
                for (let i = start; i < Math.min(5, start + req.optionalLimit); i++)
                    yield create(ReadRelationshipsResponseSchema, { relationship: rel(`d${i}`), afterResultCursor: { token: String(i + 1) } });
            },
        }));
        const request = { filter: { resourceType: "document" }, consistency: full(), pageSize: 2 };
        const { error, items } = await failure(client.readRelationships(request));
        expect(error).toBeInstanceOf(UnavailableError);
        expect(items).toHaveLength(2);
        const cursor = (error as SpiceDBError).resumeCursor;
        expect(cursor).toBe("2");
        const rest: string[] = [];
        for await (const item of client.readRelationships({ ...request, cursor }))
            rest.push(item.resource.objectId);
        expect(rest).toEqual(["d2", "d3", "d4"]);
        client.close();
    });
    it("reports the request's own cursor when nothing was delivered, and no cursor for a fresh read", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *lookupResources() { throw new ConnectError("down", Code.Unavailable); },
        }));
        const fresh = await failure(client.lookupResources(lookupRequest));
        expect((fresh.error as SpiceDBError).resumeCursor).toBeUndefined();
        const resumed = await failure(client.lookupResources({ ...lookupRequest, cursor: "c7" }));
        expect((resumed.error as SpiceDBError).resumeCursor).toBe("c7");
        client.close();
    });
    it("carries the cursor on a caller abort too", async () => {
        const controller = new AbortController();
        const client = clientFor(lookupPages(2, 10, 2));
        const { error } = await failure(client.lookupResources({ ...lookupRequest, pageSize: 2 }, { signal: controller.signal }), async () => { controller.abort("stop"); });
        expect(error).toBeInstanceOf(CancelledError);
        expect((error as SpiceDBError).resumeCursor).toBe("1");
        client.close();
    });
});

describe("malformed responses fail with Internal", () => {
    const expectInternal = (error: unknown, message: RegExp) => {
        expect(error).toBeInstanceOf(SpiceDBError);
        expect(errorCode(error)).toBe(Code.Internal);
        expect((error as Error).message).toMatch(message);
    };
    it("rejects a read response with no relationship instead of skipping it", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *readRelationships() {
                yield create(ReadRelationshipsResponseSchema, { relationship: rel("a"), afterResultCursor: { token: "1" } });
                yield create(ReadRelationshipsResponseSchema, { afterResultCursor: { token: "2" } });
            },
        }));
        const { error, items } = await failure(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }));
        expectInternal(error, /read response has no relationship/);
        expect(items).toHaveLength(1);
        client.close();
    });
    it.each([
        ["resource", create(RelationshipSchema, { relation: "viewer", subject: rel("a").subject })],
        ["subject", create(RelationshipSchema, { resource: rel("a").resource, relation: "viewer" })],
        ["relation", create(RelationshipSchema, { resource: rel("a").resource, subject: rel("a").subject })],
    ])("rejects a read relationship with no %s instead of filling it with an empty value", async (field, relationship) => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *readRelationships() { yield create(ReadRelationshipsResponseSchema, { relationship }); },
        }));
        expectInternal((await failure(client.readRelationships({ filter: { resourceType: "document" }, consistency: full() }))).error, new RegExp(`no ${field}`));
        client.close();
    });
    it("rejects malformed lookup, export, watch, expand, counter and bulk-check responses", async () => {
        const client = clientFor(router => {
            router.service(PermissionsService, {
                async *lookupResources() { yield create(LookupResourcesResponseSchema, { permissionship: LookupPermissionship.HAS_PERMISSION }); },
                async *lookupSubjects() { yield create(LookupSubjectsResponseSchema, { subject: create(ResolvedSubjectSchema, { permissionship: LookupPermissionship.HAS_PERMISSION }) }); },
                async *exportBulkRelationships() { yield create(ExportBulkRelationshipsResponseSchema, { relationships: [create(RelationshipSchema, { relation: "viewer" })] }); },
                async expandPermissionTree() { return create(ExpandPermissionTreeResponseSchema, {}); },
                async checkBulkPermissions(req) { return create(CheckBulkPermissionsResponseSchema, { pairs: req.items.map(item => create(CheckBulkPermissionsPairSchema, { request: item })) }); },
            });
            router.service(WatchService, {
                async *watch() { yield create(WatchResponseSchema, { updates: [create(RelationshipUpdateSchema, { operation: RelationshipUpdate_Operation.TOUCH })] }); },
            });
            router.service(ExperimentalService, {
                async experimentalCountRelationships() { return create(ExperimentalCountRelationshipsResponseSchema, {}); },
            });
        });
        expectInternal((await failure(client.lookupResources(lookupRequest))).error, /no resource ID/);
        expectInternal((await failure(client.lookupSubjects({ resource: { objectType: "document", objectId: "a" }, permission: "view", subjectType: "user", consistency: full() }))).error, /no subject ID/);
        expectInternal((await failure(client.exportRelationships({ consistency: full() }))).error, /export response relationship has no resource/);
        expectInternal((await failure(client.watch())).error, /watch update has no relationship/);
        expectInternal(await client.expandPermissionTree({ resource: { objectType: "document", objectId: "a" }, permission: "view", consistency: full() }).catch((e: unknown) => e), /expand response has no tree/);
        expectInternal(await client.experimentalCountRelationships("c").catch((e: unknown) => e), /neither a count nor a still-calculating/);
        expectInternal(await client.checkBulk([{ resource: { objectType: "document", objectId: "a" }, permission: "view", subject, consistency: full() }]).catch((e: unknown) => e), /malformed CheckBulkPermissionsPair/);
        client.close();
    });
    it("rejects a cursor that does not advance with Internal", async () => {
        const client = clientFor(router => router.service(PermissionsService, {
            async *lookupResources() {
                yield create(LookupResourcesResponseSchema, { resourceObjectId: "a", permissionship: LookupPermissionship.HAS_PERMISSION, afterResultCursor: { token: "same" } });
            },
        }));
        expectInternal((await failure(client.lookupResources({ ...lookupRequest, pageSize: 1, cursor: "same" }))).error, /cursor did not advance/);
        client.close();
    });
});

describe("local cancellation", () => {
    it("is the client's CancelledError, not a DOMException with a legacy numeric code", async () => {
        const client = clientFor(() => {});
        const error = await client.readSchema({ signal: AbortSignal.abort("why") }).catch((e: unknown) => e);
        expect(error).toBeInstanceOf(CancelledError);
        expect(error).not.toBeInstanceOf(DOMException);
        expect((error as CancelledError).code).toBe(Code.Canceled);
        expect(errorCode(error)).toBe(Code.Canceled);
        expect((error as Error).cause).toBe("why");
        expect((error as Error).name).toBe("AbortError");
        const doubled = await new InMemorySpiceDB().readSchema({ signal: AbortSignal.abort("why") }).catch((e: unknown) => e);
        expect(doubled).toBeInstanceOf(CancelledError);
        expect((doubled as CancelledError).code).toBe(Code.Canceled);
        client.close();
    });
});

describe("ZedToken structural equality", () => {
    it("tells different tokens apart structurally, without a custom equality tester", () => {
        expect(isDeepStrictEqual(new ZedToken("a"), new ZedToken("b"))).toBe(false);
        expect(isDeepStrictEqual(new ZedToken("a"), new ZedToken("a"))).toBe(true);
        expect(new ZedToken("a").token).toBe("a");
        expect({ ...new ZedToken("a") }).toEqual({ token: "a" });
    });
});

describe("SchemaDiff.kind", () => {
    it("is a union of the known kinds", () => {
        const kind: SchemaDiffKind = "permission_expr_changed";
        // @ts-expect-error not a schema diff kind
        const bogus: SchemaDiffKind = "permission_renamed";
        const diff: Pick<SchemaDiff, "kind"> = { kind };
        expect([diff.kind, bogus]).toHaveLength(2);
    });
});

describe("InMemorySpiceDB.diffSchema", () => {
    const base = `
        /** a user */
        definition user {}
        caveat on_tuesday(day string) { day == "tuesday" }
        definition document {
            relation viewer: user | user:*
            relation editor: user
            permission view = viewer + editor
        }`;
    it("reports no diffs for identical text, or text differing only in comments and whitespace", async () => {
        const db = new InMemorySpiceDB();
        await db.writeSchema(base);
        expect((await db.diffSchema({ comparisonSchema: base, consistency: full() })).diffs).toEqual([]);
        expect((await db.diffSchema({ comparisonSchema: base.replace("/** a user */", "").replace(/\n\s+/g, "\n"), consistency: full() })).diffs).toEqual([]);
        expect(db.callsOf("diffSchema")).toHaveLength(2);
    });
    it("diffs definitions, relations, subject types, permissions and caveats by name", async () => {
        const db = new InMemorySpiceDB();
        await db.writeSchema(base);
        const comparison = `
            definition user {}
            definition team { relation member: user }
            caveat on_tuesday(day string, tz string) { day == "tuesday" && tz == "utc" }
            definition document {
                relation viewer: user | team#member
                permission view = viewer
                permission edit = viewer
            }`;
        const { diffs, revision } = await db.diffSchema({ comparisonSchema: comparison, consistency: full() });
        expect(revision).toBeInstanceOf(ZedToken);
        expect(diffs.map(d => [d.kind, d.definitionName || d.caveatName, d.relationName || d.permissionName])).toEqual([
            ["definition_added", "team", ""],
            ["relation_removed", "document", "editor"],
            ["relation_subject_type_removed", "document", "viewer"],
            ["relation_subject_type_added", "document", "viewer"],
            ["permission_expr_changed", "document", "view"],
            ["permission_added", "document", "edit"],
            ["caveat_parameter_added", "on_tuesday", ""],
            ["caveat_expr_changed", "on_tuesday", ""],
        ]);
        expect(diffs[2]!.changedSubjectType).toEqual({ subjectDefinitionName: "user", type: { kind: "wildcard", wildcard: true } });
        expect(diffs[3]!.changedSubjectType).toEqual({ subjectDefinitionName: "team", type: { kind: "relation", relation: "member" } });
    });
    it("treats the schema before any write as empty and rejects unparseable text like the server", async () => {
        const db = new InMemorySpiceDB();
        expect((await db.diffSchema({ comparisonSchema: "definition user {}", consistency: full() })).diffs.map(d => d.kind)).toEqual(["definition_added"]);
        const error = await db.diffSchema({ comparisonSchema: "definition user {", consistency: full() }).catch((e: unknown) => e);
        expect(error).toBeInstanceOf(InvalidArgumentError);
        expect((error as SpiceDBError).message).toMatch(/^\[invalid_argument\] /);
    });
});

describe("ready()", () => {
    it("rejects with UnavailableError when the endpoint cannot be reached", async () => {
        const client = createSpiceDBClient("127.0.0.1:1", "t", { transportSecurity: TransportSecurity.Plaintext, retries: Retries.Disabled });
        await expect(client.ready({ timeoutMs: 5000 })).rejects.toBeInstanceOf(UnavailableError);
        client.close();
    });
    it("rejects with DeadlineExceededError when the server does not answer in time", async () => {
        const client = clientFor(router => router.service(SchemaService, { async readSchema() { await sleep(STALL_MS); return create(ReadSchemaResponseSchema, {}); } }));
        await expect(client.ready({ timeoutMs: 100 })).rejects.toBeInstanceOf(DeadlineExceededError);
        client.close();
    });
    it("resolves once the server answers, even without a schema, and rejects bad credentials", async () => {
        let code: Code | undefined = Code.NotFound;
        const client = clientFor(router => router.service(SchemaService, {
            async readSchema() { if (code !== undefined) throw new ConnectError("no", code); return create(ReadSchemaResponseSchema, { schemaText: "definition user {}" }); },
        }));
        await expect(client.ready()).resolves.toBeUndefined();
        code = undefined;
        await expect(client.ready()).resolves.toBeUndefined();
        code = Code.Unauthenticated;
        await expect(client.ready()).rejects.toBeInstanceOf(UnauthenticatedError);
        client.close();
    });
    it("does nothing on the double", async () => {
        await expect(new InMemorySpiceDB().ready()).resolves.toBeUndefined();
    });
});

describe("gRPC target schemes", () => {
    // Connect over Node HTTP/2 has no resolver for dns:/// targets and cannot
    // dial unix sockets, so both are refused at construction rather than
    // passed through.
    it.each(["dns:///spicedb.example.com:50051", "dns:///localhost:50051", "unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock"])("rejects %s at construction", (endpoint) => {
        for (const options of [{}, { transportSecurity: TransportSecurity.Plaintext }]) {
            let error: unknown;
            try {
                createSpiceDBClient(endpoint, "t", options).close();
            }
            catch (e) {
                error = e;
            }
            expect(error).toBeInstanceOf(InvalidArgumentError);
            expect((error as Error).message).toMatch(/host:port/);
        }
    });
});

// Compile-time: every operation accepts a per-call deadline.
type LastParam<F> = F extends (...args: infer A) => unknown ? A extends [...unknown[], infer L] ? L : never : never;
type AcceptsDeadline = { [K in Exclude<keyof SpiceDBClientInterface, "raw" | "close">]: "timeoutMs" extends keyof NonNullable<LastParam<SpiceDBClientInterface[K]>> ? true : false };
const everyOperationAcceptsDeadline: AcceptsDeadline[keyof AcceptsDeadline] extends true ? true : false = true;
describe("per-call deadlines", () => {
    it("are accepted by every operation", () => { expect(everyOperationAcceptsDeadline).toBe(true); });
});
