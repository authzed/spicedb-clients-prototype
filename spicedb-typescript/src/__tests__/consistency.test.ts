import { ZedToken } from "../types.js";
import { describe, it, expect } from "vitest";
import { Consistency, full, minLatency, atLeast, atLeastOrFull, atLeastOrMinLatency, snapshot, } from "../consistency.js";
describe("consistency strategies", () => {
    it("full() returns a native Consistency instance (not the proto type)", () => {
        const c = full();
        expect(c).toBeInstanceOf(Consistency);
        // The native wrapper must not expose the proto's `requirement` field
        // directly -- it is opaque until unwrapped via _toProto().
        expect((c as unknown as {
            requirement?: unknown;
        }).requirement).toBeUndefined();
    });
    it("full() creates fully consistent requirement", () => {
        const c = full();
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("fullyConsistent");
        expect(proto.requirement.value).toBe(true);
    });
    it("minLatency() returns a native Consistency instance", () => {
        const c = minLatency();
        expect(c).toBeInstanceOf(Consistency);
    });
    it("minLatency() creates minimize latency requirement", () => {
        const c = minLatency();
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("minimizeLatency");
        expect(proto.requirement.value).toBe(true);
    });
    it("atLeast() returns a native Consistency instance", () => {
        const c = atLeast(new ZedToken("some-token"));
        expect(c).toBeInstanceOf(Consistency);
    });
    it("atLeast() creates at-least-as-fresh requirement", () => {
        const c = atLeast(new ZedToken("some-token"));
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("atLeastAsFresh");
        if (proto.requirement.case === "atLeastAsFresh") {
            expect(proto.requirement.value.token).toBe("some-token");
        }
    });
    it("atLeastOrFull() with revision returns atLeastAsFresh", () => {
        const c = atLeastOrFull(new ZedToken("some-token"));
        expect(c).toBeInstanceOf(Consistency);
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("atLeastAsFresh");
        if (proto.requirement.case === "atLeastAsFresh") {
            expect(proto.requirement.value.token).toBe("some-token");
        }
    });
    it("atLeastOrFull() with empty string returns fullyConsistent", () => {
        const c = atLeastOrFull(undefined);
        expect(c).toBeInstanceOf(Consistency);
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("fullyConsistent");
        expect(proto.requirement.value).toBe(true);
    });
    it("atLeastOrMinLatency() with revision returns atLeastAsFresh", () => {
        const c = atLeastOrMinLatency(new ZedToken("some-token"));
        expect(c).toBeInstanceOf(Consistency);
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("atLeastAsFresh");
        if (proto.requirement.case === "atLeastAsFresh") {
            expect(proto.requirement.value.token).toBe("some-token");
        }
    });
    it("atLeastOrMinLatency() with empty string returns minimizeLatency", () => {
        const c = atLeastOrMinLatency(undefined);
        expect(c).toBeInstanceOf(Consistency);
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("minimizeLatency");
        expect(proto.requirement.value).toBe(true);
    });
    it("snapshot() returns a native Consistency instance", () => {
        const c = snapshot(new ZedToken("some-token"));
        expect(c).toBeInstanceOf(Consistency);
    });
    it("snapshot() creates at-exact-snapshot requirement", () => {
        const c = snapshot(new ZedToken("some-token"));
        const proto = c._toProto();
        expect(proto.requirement.case).toBe("atExactSnapshot");
        if (proto.requirement.case === "atExactSnapshot") {
            expect(proto.requirement.value.token).toBe("some-token");
        }
    });
});
