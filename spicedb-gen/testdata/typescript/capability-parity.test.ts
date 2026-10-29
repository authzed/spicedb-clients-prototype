import { expect, it, vi } from "vitest";
import { SpiceDBClient, TransportSecurity, ZedToken, full, Tracing, LookupDebug, LookupPermissionshipFilter, Wildcards, type DeleteResult } from "@spicedb/client";
import { TypedClient, Document, User, Team } from "./permissions";

type Assert<T extends true> = T;
// New public native capabilities must also exist directly on the generated facade.
type AllNativeCapabilities = Assert<Exclude<keyof SpiceDBClient, keyof TypedClient> extends never ? true : false>;
const covered: AllNativeCapabilities = true;
const setup = () => new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });

it("exposes all public native capability names", () => { expect(covered).toBe(true); });
it("forwards deletion options, result identity and failure progress", async () => {
    const native = setup(); const typed = new TypedClient(native);
    const filter = { resourceType: "document", resourceId: "readme" };
    const options = { pageSize: 2, cursor: "opaque+==", signal: new AbortController().signal };
    const result: DeleteResult = Object.freeze({ revision: new ZedToken("revision"), deletedCount: 2n, progress: "complete" });
    const promise = Promise.resolve(result);
    const all = vi.spyOn(native, "deleteAllRelationshipsNonAtomic").mockReturnValue(promise);
    const bounded = vi.spyOn(native, "deleteRelationships").mockResolvedValue(result);
    try {
        expect(typed.deleteAllRelationshipsNonAtomic(filter, options)).toBe(promise);
        expect(all).toHaveBeenCalledWith(filter, options);
        expect(await typed.deleteRelationships(filter, { limit: 2 })).toBe(result);
        expect(bounded).toHaveBeenCalledWith(filter, { limit: 2 });
        const error = Object.assign(new Error("interrupted"), { deleteProgress: result });
        all.mockRejectedValue(error);
        await expect(typed.deleteAllRelationshipsNonAtomic(filter, options)).rejects.toBe(error);
    } finally { typed.close(); }
});
it("returns the native watch iterator and forwards full request/cancellation", async () => {
    const native = setup(); const typed = new TypedClient(native);
    let released = false;
    const iterator = (async function* () { try { yield { updates: [], changesThrough: new ZedToken("cursor"), isCheckpoint: true, schemaUpdated: false }; } finally { released = true; } })();
    const watch = vi.spyOn(native, "watch").mockReturnValue(iterator);
    const request = { relationshipFilters: [{ resourceType: "document", relation: "viewer" }], startRevision: new ZedToken("resume") };
    const options = { signal: new AbortController().signal };
    try {
        const events = typed.watch(request, options);
        expect(events).toBe(iterator);
        expect(watch).toHaveBeenCalledWith(request, options);
        for await (const _event of events) break;
        expect(released).toBe(true);
    } finally { typed.close(); }
});
it("forwards producers, bulk options, and experimental results unchanged", async () => {
    const native = setup(); const typed = new TypedClient(native);
    const producer = (async function* () {})();
    const upload = vi.spyOn(native, "importRelationships").mockResolvedValue(0n);
    const checks = [{ resource: { objectType: "document", objectId: "one" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() }];
    const bulk = vi.spyOn(native, "checkBulk").mockResolvedValue([]);
    const count = { stillCalculating: false, count: 2n, revision: new ZedToken("r") };
    vi.spyOn(native, "experimentalCountRelationships").mockResolvedValue(count);
    try {
        expect(await typed.importRelationships(producer)).toBe(0n);
        expect(upload).toHaveBeenCalledWith(producer);
        await typed.checkBulk(checks, { tracing: Tracing.Enabled });
        expect(bulk).toHaveBeenCalledWith(checks, { tracing: Tracing.Enabled });
        expect(await typed.experimentalCountRelationships("counter")).toBe(count);
    } finally { typed.close(); }
});
it("preserves every lookup/read option with schema-aware selectors", () => {
    const native = setup(); const typed = new TypedClient(native);
    const iterator = (async function* () {})();
    const resources = vi.spyOn(native, "lookupResources").mockReturnValue(iterator);
    const subjects = vi.spyOn(native, "lookupSubjects").mockReturnValue(iterator);
    const read = vi.spyOn(native, "readRelationships").mockReturnValue(iterator);
    try {
        typed.lookupResources(full(), Document.view, User("alice"), { context: { region: "us" }, limit: 3, pageSize: 2, cursor: "next", debug: LookupDebug.Enabled, permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly });
        expect(resources.mock.calls[0]![0]).toMatchObject({ limit: 3, pageSize: 2, cursor: "next", debug: LookupDebug.Enabled, context: { region: "us" } });
        typed.lookupSubjects(full(), Document("one").view, Team, { wildcards: Wildcards.Exclude, limit: 3, cursor: "next", subjectRelation: "member", context: { region: "us" } });
        expect(subjects.mock.calls[0]![0]).toMatchObject({ wildcards: Wildcards.Exclude, limit: 3, cursor: "next", subjectRelation: "member", context: { region: "us" } });
        typed.readRelationships(full(), { resourceType: "document", resourceIdPrefix: "team/", relation: "viewer" }, { pageSize: 2, cursor: "next" });
        expect(read.mock.calls[0]![0]).toMatchObject({ filter: { resourceType: "document", resourceIdPrefix: "team/", relation: "viewer" }, pageSize: 2, cursor: "next" });
    } finally { typed.close(); }
});
