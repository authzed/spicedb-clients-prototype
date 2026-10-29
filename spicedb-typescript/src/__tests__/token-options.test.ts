import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { CheckPermissionResponseSchema, ZedTokenSchema } from "@spicedb/proto";
import { SpiceDBClient, ZedToken, Tracing, LookupDebug, Wildcards, WatchContent, TransportSecurity, RemoteCredentials, full, atLeast, InvalidArgumentError } from "../index.js";

const local = () => new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
const request = () => ({ resource: { objectType: "document", objectId: "one" }, permission: "view", subject: { object: { objectType: "user", objectId: "alice" } }, consistency: full() });

describe("native tokens and behavioral modes", () => {
    it("owns an opaque immutable token and serializes it losslessly", () => {
        const raw = "revision/雪=+\u0000";
        const token = new ZedToken(raw);
        expect(token.toString()).toBe(raw);
        expect(token.equals(new ZedToken(raw))).toBe(true);
        expect(token.equals(new ZedToken("different"))).toBe(false);
        expect(JSON.stringify(token)).toBe(JSON.stringify(raw));
        expect(Object.isFrozen(token)).toBe(true);
        expect(Object.keys(token)).toEqual(["token"]);
        expect(token.token).toBe(raw);
        expect(atLeast(token)._toProto().requirement.value).toEqual(create(ZedTokenSchema, { token: raw }));
        expect(() => atLeast(raw as unknown as ZedToken)).toThrow(InvalidArgumentError);
    });
    it("returns runtime tokens and maps named tracing choices", async () => {
        const c = local();
        const rpc = vi.spyOn(c.raw().permissions, "checkPermission").mockResolvedValue(create(CheckPermissionResponseSchema, { checkedAt: { token: "revision" } }));
        const result = await c.check(request(), { tracing: Tracing.Enabled });
        expect(result.checkedAt).toBeInstanceOf(ZedToken);
        expect(result.checkedAt.toString()).toBe("revision");
        expect(rpc.mock.calls[0][0].withTracing).toBe(true);
        await c.check(request());
        expect(rpc.mock.calls[1][0].withTracing).toBe(false);
        await expect(c.check(request(), { tracing: "future" as Tracing })).rejects.toThrow(InvalidArgumentError);
        expect(rpc).toHaveBeenCalledTimes(2);
        c.close();
    });
    it("maps lookup and watch enum choices to their exact protocol values", async () => {
        const c = local();
        const resources = vi.spyOn(c.raw().permissions, "lookupResources").mockImplementation(async function* () {});
        await c.lookupResources({ resourceType: "document", permission: "view", subject: request().subject, consistency: full(), debug: LookupDebug.Enabled }).next();
        expect(resources.mock.calls[0][0].withDebug).toBe(true);
        const subjects = vi.spyOn(c.raw().permissions, "lookupSubjects").mockImplementation(async function* () {});
        await c.lookupSubjects({ resource: request().resource, permission: "view", subjectType: "user", consistency: full(), wildcards: Wildcards.Exclude }).next();
        expect(subjects.mock.calls[0][0].wildcardOption).toBe(2);
        const watch = vi.spyOn(c.raw().watch, "watch").mockImplementation(async function* () {});
        await c.watch({ content: [WatchContent.Schema, WatchContent.Checkpoints] }).next();
        expect(watch.mock.calls[0][0].optionalUpdateKinds).toEqual([2, 3]);
        c.close();
    });
    it("rejects unknown lookup and watch modes before starting calls", () => {
        const c = local();
        expect(() => c.lookupResources({ resourceType: "document", permission: "view", subject: request().subject, consistency: full(), debug: "future" as LookupDebug })).toThrow(InvalidArgumentError);
        expect(() => c.lookupSubjects({ resource: request().resource, permission: "view", subjectType: "user", consistency: full(), wildcards: "future" as Wildcards })).toThrow(InvalidArgumentError);
        expect(() => c.watch({ content: ["future" as WatchContent] })).toThrow(InvalidArgumentError);
        c.close();
    });
    it("permits remote plaintext only through an explicit public policy", () => {
        const options = { endpoint: "remote.example.invalid:50051", token: "secret", transportSecurity: TransportSecurity.Plaintext };
        expect(() => new SpiceDBClient(options)).toThrow(InvalidArgumentError);
        const c = new SpiceDBClient({ ...options, remoteCredentials: RemoteCredentials.AllowPlaintext });
        c.close();
        expect(() => new SpiceDBClient({ ...options, remoteCredentials: "future" as RemoteCredentials })).toThrow(InvalidArgumentError);
    });
});
