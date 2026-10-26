import "./token-equality.js";
import { Tracing, TransportSecurity } from "../modes.js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { CheckPermissionResponseSchema, CheckBulkPermissionsRequestItemSchema, CheckBulkPermissionsPairSchema, CheckBulkPermissionsResponseItemSchema, ZedTokenSchema, CheckBulkPermissionsResponseSchema, DebugInformationSchema, DeleteRelationshipsResponseSchema, DeleteRelationshipsResponse_DeletionProgress, ReflectionDefinitionSchema, ReflectionSchemaDiffSchema } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { full, minLatency, atLeast } from "../consistency.js";
import { DeadlineExceededError, InvalidArgumentError, PermissionDeniedError, CancelledError } from "../errors.js";
import { fromProtoSchemaDefinition, fromProtoSchemaDiff, relationship, Transaction, ZedToken, type CallOptions, type CheckRequest, type CheckOptions } from "../types.js";
const request = (): CheckRequest => ({ resource: { objectType: "document", objectId: "1" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() });
const client = () => new SpiceDBClient({ endpoint: "localhost:1", token: "t", transportSecurity: TransportSecurity.Plaintext });
const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));
const grant = () => create(CheckPermissionResponseSchema, { permissionship: 2, checkedAt: { token: "checked" } });
const batch = (items: readonly MessageInitShape<typeof CheckBulkPermissionsRequestItemSchema>[], revision = "chunk") => create(CheckBulkPermissionsResponseSchema, { checkedAt: create(ZedTokenSchema, { token: revision }), pairs: items.map(item => create(CheckBulkPermissionsPairSchema, { request: item, response: { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: 2 }) } })) });
afterEach(() => vi.restoreAllMocks());
describe("canonical checks and immutable snapshots", () => {
    it("rejects mixed modes, missing consistency, and unequal tokens before any RPC", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions");
        for (const consistency of [undefined, minLatency(), atLeast(new ZedToken("different"))])
            await expect(c.checkBulk([request(), { ...request(), consistency: consistency as never }])).rejects.toBeInstanceOf(InvalidArgumentError);
        expect(rpc).not.toHaveBeenCalled();
        c.close();
    });
    it("accepts separately constructed equal consistency and preserves chunk revisions", async () => {
        const c = client();
        let count = 0;
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => batch(req.items ?? [], `chunk-${++count}`));
        const results = await c.checkBulk(Array.from({ length: 1001 }, request));
        expect(rpc).toHaveBeenCalledTimes(2);
        expect(results[999].checkedAt.toString()).toBe("chunk-1");
        expect(results[1000].checkedAt.toString()).toBe("chunk-2");
        expect(Object.isFrozen(results)).toBe(true);
        c.close();
    });
    it("snapshots every chunk and merges defaults by key before the first RPC", async () => {
        const c = client();
        let release!: () => void;
        const gate = new Promise<void>(resolve => release = resolve);
        const context = { nested: { values: [null, 1] }, region: "item" };
        const defaults = { region: "default", other: { value: true } };
        const requests = Array.from({ length: 1001 }, () => ({ ...request(), context }));
        let count = 0;
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => {
            if (++count === 1)
                await gate;
            return batch(req.items ?? []);
        });
        const result = c.checkBulk(requests, { context: defaults });
        context.nested.values.push(2);
        defaults.other.value = false;
        requests[1000].resource.objectId;
        release();
        await result;
        expect(rpc.mock.calls[1][0].items?.[0].context).toEqual({ nested: { values: [null, 1] }, region: "item", other: { value: true } });
        c.close();
    });
    it("rejects invalid JSON even in a later chunk before sending earlier chunks", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions");
        const requests = Array.from({ length: 1001 }, request);
        requests[1000] = { ...request(), context: { nested: { bad: undefined } } as never };
        await expect(c.checkBulk(requests)).rejects.toThrow(/context.nested.bad/);
        expect(rpc).not.toHaveBeenCalled();
        c.close();
    });
    it("retains typed per-item error index, source status, and ErrorInfo", async () => {
        const c = client();
        const response = create(CheckBulkPermissionsResponseSchema, { pairs: [{ response: { case: "error", value: { code: 7, message: "denied" } } }] });
        vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => { response.pairs[0].request = create(CheckBulkPermissionsRequestItemSchema, req.items![0]); return response; });
        try {
            await c.checkBulk([request()]);
            throw new Error("expected error");
        }
        catch (error) {
            expect(error).toBeInstanceOf(PermissionDeniedError);
            expect((error as PermissionDeniedError).inputIndex).toBe(0);
            expect((error as PermissionDeniedError).cause).toBe(response.pairs[0].response.value);
        }
        c.close();
    });
});
describe("bulk response identity", () => {
    it.each(["missing", "reversed", "duplicated"])("rejects %s request echoes", async (kind) => {
        const c = client();
        vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => {
            const items = req.items ?? [];
            const echoed = kind === "missing" ? [undefined, undefined] : kind === "reversed" ? [...items].reverse() : [items[0], items[0]];
            return create(CheckBulkPermissionsResponseSchema, { pairs: echoed.map(item => create(CheckBulkPermissionsPairSchema, {
                request: item, response: { case: "item", value: { permissionship: 2 } },
            })) });
        });
        await expect(c.checkBulk([request(), { ...request(), permission: "edit" }])).rejects.toThrow(/echoed request/);
        c.close();
    });
});
describe("opt-in native check tracing", () => {
    const debug = () => create(DebugInformationSchema, { schemaUsed: "definition document {}", check: { resource: { objectType: "document", objectId: "1" }, permission: "view", permissionType: 2, subject: { object: { objectType: "user", objectId: "alice" } }, result: 2, duration: { seconds: 1n, nanos: 23 }, resolution: { case: "subProblems", value: { traces: [{ permission: "viewer", permissionType: 1, result: 3, caveatEvaluationInfo: { expression: "x == 1", result: 4, context: { x: [null, { flag: true }] }, partialCaveatInfo: { missingRequiredContext: ["y"] }, caveatName: "test" }, resolution: { case: "wasCachedResult", value: false }, optionalExpiresAt: { seconds: 1767225600n, nanos: 0 }, traceOperationId: "child-id", source: "spicedb:child" }] } }, traceOperationId: "root-id", source: "spicedb:root" } });
    it("sends the singular flag and converts every recursive diagnostic field", async () => {
        const c = client();
        const wire = debug();
        const rpc = vi.spyOn(c.raw().permissions, "checkPermission").mockResolvedValue(create(CheckPermissionResponseSchema, { ...grant(), debugTrace: wire }));
        const result = await c.check(request(), { tracing: Tracing.Enabled });
        expect(rpc.mock.calls[0][0].withTracing).toBe(true);
        expect(result.hasPermission()).toBe(true);
        const trace = result.debugTrace!;
        expect(trace.schemaUsed).toBe("definition document {}");
        expect(trace.check).toMatchObject({ resource: request().resource, subject: request().subject, permission: "view", permissionType: "permission", result: "hasPermission", durationNs: 1000000023n, traceOperationId: "root-id", source: "spicedb:root" });
        if (trace.check?.resolution.kind !== "subproblems")
            throw new Error("missing recursive trace");
        const child = trace.check.resolution.traces[0];
        expect(child).toMatchObject({ permission: "viewer", permissionType: "relation", result: "conditionalPermission", caveatEvaluation: { expression: "x == 1", result: "missingContext", context: { x: [null, { flag: true }] }, missingContext: ["y"], caveatName: "test" }, resolution: { kind: "cached", cached: false }, traceOperationId: "child-id", source: "spicedb:child" });
        expect(child.expiration?.toISOString()).toBe("2026-01-01T00:00:00.000Z");
        wire.check!.resolution = { case: undefined };
        expect(trace.check.resolution.kind).toBe("subproblems");
        expect(Object.isFrozen(child.caveatEvaluation?.context)).toBe(true);
        c.close();
    });
    it("defaults tracing false, preserves absent trace, and never grants unknown enums", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "checkPermission").mockResolvedValue(create(CheckPermissionResponseSchema, { permissionship: 999 as never, debugTrace: { check: { permissionType: 999 as never, result: 999 as never, resolution: { case: undefined } } } }));
        const result = await c.check(request());
        expect(rpc.mock.calls[0][0].withTracing).toBe(false);
        expect(result.hasPermission()).toBe(false);
        expect(result.debugTrace?.check).toMatchObject({ permissionType: "unspecified", result: "unspecified", resolution: { kind: "unspecified" } });
        rpc.mockResolvedValue(grant());
        expect((await c.check(request())).debugTrace).toBeUndefined();
        c.close();
    });
    it("sets tracing on every bulk chunk and maps each item's own trace", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => create(CheckBulkPermissionsResponseSchema, { checkedAt: create(ZedTokenSchema, { token: "chunk" }), pairs: (req.items ?? []).map((item, i) => create(CheckBulkPermissionsPairSchema, { request: item, response: { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: 2, debugTrace: create(DebugInformationSchema, { schemaUsed: `item-${i}` }) }) } })) }));
        const results = await c.checkBulk(Array.from({ length: 1001 }, request), { tracing: Tracing.Enabled });
        expect(rpc.mock.calls.every(call => call[0].withTracing === true)).toBe(true);
        expect(results[0].debugTrace?.schemaUsed).toBe("item-0");
        expect(results[999].debugTrace?.schemaUsed).toBe("item-999");
        expect(results[1000].debugTrace?.schemaUsed).toBe("item-0");
        c.close();
    });
});
describe("one total operation deadline", () => {
    it("expires during retry backoff before a second attempt", async () => {
        vi.spyOn(Math, "random").mockReturnValue(1);
        const c = client();
        const rpc = vi.spyOn(c.raw().schema, "readSchema").mockRejectedValue(new ConnectError("retry", Code.Unavailable));
        const start = performance.now();
        await expect(c.readSchema({ timeoutMs: 25 })).rejects.toBeInstanceOf(DeadlineExceededError);
        expect(performance.now() - start).toBeLessThan(90);
        expect(rpc).toHaveBeenCalledTimes(1);
        c.close();
    });
    it("passes only remaining time to later bulk chunks", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => { await sleep(35); return batch(req.items ?? []); });
        await expect(c.checkBulk(Array.from({ length: 1001 }, request), { timeoutMs: 55 })).rejects.toBeInstanceOf(DeadlineExceededError);
        expect(rpc).toHaveBeenCalledTimes(2);
        expect(rpc.mock.calls[1][1]?.timeoutMs).toBeLessThan(35);
        c.close();
    });
    it("rejects unknown delete-all progress and preserves the acknowledged count", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "deleteRelationships").mockResolvedValue(create(DeleteRelationshipsResponseSchema, {
            deletedAt: { token: "acknowledged" }, relationshipsDeletedCount: 2n,
            deletionProgress: 4242 as DeleteRelationshipsResponse_DeletionProgress,
        }));
        await expect(c.deleteAllRelationshipsNonAtomic({ resourceType: "document" })).rejects.toMatchObject({
            message: expect.stringMatching(/progress/i),
            deleteProgress: { revision: new ZedToken("acknowledged"), deletedCount: 2n, progress: "unspecified" },
        });
        expect(rpc).toHaveBeenCalledTimes(1);
        c.close();
    });
    it("retains acknowledged cumulative delete progress when a later page times out", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "deleteRelationships").mockImplementation(async () => { await sleep(35); return create(DeleteRelationshipsResponseSchema, { deletedAt: { token: "page-1" }, deletionProgress: DeleteRelationshipsResponse_DeletionProgress.PARTIAL, relationshipsDeletedCount: 10n, afterResultCursor: { token: "cursor" } }); });
        try {
            await c.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, { pageSize: 10, timeoutMs: 55 });
            throw new Error("expected deadline");
        }
        catch (error) {
            expect(error).toBeInstanceOf(DeadlineExceededError);
            expect((error as DeadlineExceededError).deleteProgress).toEqual({ revision: new ZedToken("page-1"), deletedCount: 10n, cursor: "cursor", progress: "partial" });
        }
        expect(rpc).toHaveBeenCalledTimes(2);
        expect(rpc.mock.calls[1][1]?.timeoutMs).toBeLessThan(35);
        c.close();
    });
    it("counts consumer idle time from stream method invocation", async () => {
        const c = client();
        const rpc = vi.spyOn(c.raw().permissions, "readRelationships");
        const iterator = c.readRelationships({ filter: { resourceType: "document" }, consistency: full() }, { timeoutMs: 15 });
        await sleep(30);
        await expect(iterator.next()).rejects.toBeInstanceOf(DeadlineExceededError);
        expect(rpc).not.toHaveBeenCalled();
        c.close();
    });
});
describe("native cancellation across every operation", () => {
    const calls: readonly [
        string,
        (c: SpiceDBClient, options: CallOptions) => Promise<unknown> | AsyncIterableIterator<unknown>
    ][] = [
        ["check", (c, o) => c.check(request(), o)], ["checkBulk", (c, o) => c.checkBulk([request()], o)], ["write", (c, o) => c.write(new Transaction(), o)],
        ["readRelationships", (c, o) => c.readRelationships({ filter: { resourceType: "document" }, consistency: full() }, o)],
        ["deleteRelationships", (c, o) => c.deleteRelationships({ resourceType: "document" }, { ...o, limit: 1 })], ["deleteAllRelationshipsNonAtomic", (c, o) => c.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, o)],
        ["lookupResources", (c, o) => c.lookupResources({ resourceType: "document", permission: "view", subject: request().subject, consistency: full() }, o)],
        ["lookupSubjects", (c, o) => c.lookupSubjects({ resource: request().resource, permission: "view", subjectType: "user", consistency: full() }, o)],
        ["expandPermissionTree", (c, o) => c.expandPermissionTree({ ...request() }, o)], ["readSchema", (c, o) => c.readSchema(o)], ["writeSchema", (c, o) => c.writeSchema("", o)],
        ["reflectSchema", (c, o) => c.reflectSchema({ consistency: full() }, o)], ["computablePermissions", (c, o) => c.computablePermissions({ definitionName: "document", relationName: "viewer", consistency: full() }, o)],
        ["dependentRelations", (c, o) => c.dependentRelations({ definitionName: "document", permissionName: "view", consistency: full() }, o)], ["diffSchema", (c, o) => c.diffSchema({ comparisonSchema: "", consistency: full() }, o)],
        ["importRelationships", (c, o) => c.importRelationships([], o)], ["exportRelationships", (c, o) => c.exportRelationships({ consistency: full() }, o)], ["watch", (c, o) => c.watch({}, o)],
        ["experimentalRegisterRelationshipCounter", (c, o) => c.experimentalRegisterRelationshipCounter("test", { resourceType: "document" }, o)],
        ["experimentalCountRelationships", (c, o) => c.experimentalCountRelationships("test", o)], ["experimentalUnregisterRelationshipCounter", (c, o) => c.experimentalUnregisterRelationshipCounter("test", o)],
        ["experimentalRoaringLookupResources", (c, o) => c.experimentalRoaringLookupResources({ resourceType: "document", permission: "view", subject: request().subject, consistency: full() }, o)],
        ["experimentalCountRelationshipsByFilter", (c, o) => c.experimentalCountRelationshipsByFilter({ resourceType: "document" }, o)],
        ["experimentalWatchPermissionSets", (c, o) => c.experimentalWatchPermissionSets({}, o)],
        ["experimentalLookupPermissionSets", (c, o) => c.experimentalLookupPermissionSets({}, o)],
        ["experimentalDownloadPermissionSets", (c, o) => c.experimentalDownloadPermissionSets({}, o)],
    ];
    it.each(calls)("%s performs no RPC for an already aborted signal", async (_name, invoke) => {
        const c = client();
        const spies = Object.values(c.raw()).flatMap(group => group && typeof group === "object" ? Object.keys(group).filter(key => typeof (group as Record<string, unknown>)[key] === "function").map(key => vi.spyOn(group as Record<string, () => unknown>, key)) : []);
        const reason = Object.freeze({ reason: "caller" });
        const signal = AbortSignal.abort(reason);
        const value = invoke(c, { signal });
        const promise = Symbol.asyncIterator in value ? value.next() : value;
        try {
            await promise;
            throw new Error("expected abort");
        }
        catch (error) {
            expect(error).toBeInstanceOf(DOMException);
            expect((error as DOMException).name).toBe("AbortError");
            expect((error as Error).cause).toBe(reason);
        }
        expect(spies.every(spy => spy.mock.calls.length === 0)).toBe(true);
        c.close();
    });
    it.each(calls)("%s forwards cancellation to active transport work", async (_name, invoke) => {
        const c = client();
        let transportSignal: AbortSignal | undefined;
        const streaming = new Set(["readRelationships", "lookupResources", "lookupSubjects", "exportBulkRelationships", "watch", "watchPermissionSets", "lookupPermissionSets"]);
        for (const group of [c.raw().permissions, c.raw().schema, c.raw().watch, c.raw().experimental, c.raw().materialize, c.raw().materializeRelationships, c.raw().permissionSets]) {
            const methods = group as unknown as Record<string, (...args: unknown[]) => unknown>;
            for (const key of Object.keys(methods))
                vi.spyOn(methods, key).mockImplementation((...args) => {
                    transportSignal = (args[1] as {
                        signal?: AbortSignal;
                    } | undefined)?.signal;
                    if (streaming.has(key))
                        return (async function* () { await new Promise(() => { }); yield undefined; })();
                    return new Promise(() => { });
                });
        }
        const controller = new AbortController();
        const value = invoke(c, { signal: controller.signal });
        const promise = Symbol.asyncIterator in value ? value.next() : value;
        const result = promise.then(() => undefined, error => error as unknown);
        await sleep(0);
        expect(transportSignal).toBeDefined();
        controller.abort("active");
        expect(await result).toMatchObject({ name: "AbortError", cause: "active" });
        expect(transportSignal?.aborted).toBe(true);
        c.close();
    });
    it("aborts backoff promptly with cause=signal.reason", async () => {
        const c = client();
        const controller = new AbortController();
        const reason = new Error("caller reason");
        vi.spyOn(Math, "random").mockReturnValue(1);
        const rpc = vi.spyOn(c.raw().schema, "readSchema").mockRejectedValue(new ConnectError("retry", Code.Aborted));
        const result = c.readSchema({ signal: controller.signal });
        await sleep(5);
        controller.abort(reason);
        await expect(result).rejects.toMatchObject({ name: "AbortError", cause: reason });
        expect(rpc).toHaveBeenCalledTimes(1);
        expect(reason).not.toHaveProperty("deleteProgress");
        c.close();
    });
    it("stops before later bulk chunks after cancellation", async () => {
        const c = client();
        const controller = new AbortController();
        const rpc = vi.spyOn(c.raw().permissions, "checkBulkPermissions").mockImplementation(async (req) => { controller.abort("between chunks"); return batch(req.items ?? []); });
        await expect(c.checkBulk(Array.from({ length: 1001 }, request), { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError", cause: "between chunks" });
        expect(rpc).toHaveBeenCalledTimes(1);
        c.close();
    });
    it("retains delete progress on native AbortError without modifying caller reason", async () => {
        const c = client();
        const controller = new AbortController();
        const reason = Object.freeze({ reason: "done" });
        let calls = 0;
        vi.spyOn(c.raw().permissions, "deleteRelationships").mockImplementation(async () => {
            if (++calls === 2) {
                controller.abort(reason);
                await sleep(5);
            }
            return create(DeleteRelationshipsResponseSchema, { deletedAt: { token: "acknowledged" }, deletionProgress: 2, relationshipsDeletedCount: 2n });
        });
        try {
            await c.deleteAllRelationshipsNonAtomic({ resourceType: "document" }, { signal: controller.signal, pageSize: 2 });
            throw new Error("expected abort");
        }
        catch (error) {
            expect(error).toBeInstanceOf(DOMException);
            expect(error).toMatchObject({ name: "AbortError", cause: reason, deleteProgress: { deletedCount: 2n, revision: new ZedToken("acknowledged"), progress: "partial" } });
        }
        expect(reason).not.toHaveProperty("deleteProgress");
        c.close();
    });
    it("cancels an upload while awaiting an input producer", async () => {
        const c = client();
        const controller = new AbortController();
        let signal: AbortSignal | undefined;
        vi.spyOn(c.raw().permissions, "importBulkRelationships").mockImplementation(async (input, options) => { signal = options?.signal; for await (const _ of input) { } return { numLoaded: 0n } as never; });
        async function* input() { await new Promise(() => { }); yield relationship("document:a", "viewer", "user:a"); }
        const result = c.importRelationships(input(), { signal: controller.signal });
        await sleep(1);
        controller.abort("producer");
        await expect(result).rejects.toMatchObject({ name: "AbortError", cause: "producer" });
        expect(signal?.aborted).toBe(true);
        c.close();
    });
    it("preserves a server-originated cancelled status as CancelledError", async () => {
        const c = client();
        const status = new ConnectError("server", Code.Canceled);
        vi.spyOn(c.raw().schema, "readSchema").mockRejectedValue(status);
        await expect(c.readSchema()).rejects.toMatchObject({ name: "CancelledError", cause: status });
        c.close();
    });
});
describe("full native protocol payloads", () => {
    it("preserves reflection subject types including false oneof presence", () => {
        const result = fromProtoSchemaDefinition(create(ReflectionDefinitionSchema, { name: "document", relations: [{ name: "viewer", subjectTypes: [{ subjectDefinitionName: "user", optionalCaveatName: "test", typeref: { case: "isTerminalSubject", value: false } }] }] }));
        expect(result.relations[0].subjectTypes).toEqual([{ subjectDefinitionName: "user", caveatName: "test", type: { kind: "terminal", terminal: false } }]);
    });
    it("preserves changed subject type and previous caveat parameter type", () => {
        const subject = fromProtoSchemaDiff(create(ReflectionSchemaDiffSchema, { diff: { case: "relationSubjectTypeAdded", value: { changedSubjectType: { subjectDefinitionName: "user", typeref: { case: "isPublicWildcard", value: true } } } } }));
        expect(subject.changedSubjectType?.type).toEqual({ kind: "wildcard", wildcard: true });
        const parameter = fromProtoSchemaDiff(create(ReflectionSchemaDiffSchema, { diff: { case: "caveatParameterTypeChanged", value: { parameter: { name: "a", type: "bool", parentCaveatName: "test" }, previousType: "int" } } }));
        expect(parameter.parameter?.type).toBe("bool");
        expect(parameter.previousType).toBe("int");
    });
    it("preserves 64-bit roaring payloads and copies bitmap ownership", async () => {
        const c = client();
        const bitmap = new Uint8Array([1, 2, 3]);
        vi.spyOn(c.raw().materialize, "experimentalRoaringLookupResources").mockResolvedValue({ bitmap, cardinality: 9007199254740993n, atRevision: { token: "bitmap" } } as never);
        const result = await c.experimentalRoaringLookupResources({ ...request(), resourceType: "document" });
        bitmap[0] = 99;
        expect(result.bitmap).toEqual(new Uint8Array([1, 2, 3]));
        expect(result.cardinality).toBe(9007199254740993n);
        expect(result.revision.toString()).toBe("bitmap");
        c.close();
    });
});
// Compile-time migration contract: readonly inputs and distinct data/check shapes.
function typeAssertions(c: SpiceDBClient, check: CheckRequest, stored: import("../types.js").Relationship, options: CheckOptions) {
    // @ts-expect-error requests are readonly
    check.permission = "edit";
    // @ts-expect-error persisted data has no check context
    stored.context = {};
    // @ts-expect-error relationships are not checks
    void c.check(stored);
    // @ts-expect-error tokens are opaque branded values
    atLeast("plain-string");
    void c.checkBulk([check] as const, options);
}
void typeAssertions;
