import { describe, it, expect, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { WriteRelationshipsResponseSchema } from "@spicedb/proto";
import { SpiceDBClient, TransportSecurity, Transaction, ZedToken } from "../index.js";

describe("relationship write result", () => {
    it.each(["opaque/雪=+\u0000", "", undefined])("wraps revision %s in an immutable native result", async (text) => {
        const client = new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
        vi.spyOn(client.raw().permissions, "writeRelationships").mockResolvedValue(create(WriteRelationshipsResponseSchema, { writtenAt: text === undefined ? undefined : { token: text } }));
        try {
            const result = await client.write(new Transaction());
            expect(result).not.toBeInstanceOf(ZedToken);
            expect(result).toHaveProperty("revision");
            expect(result.revision).toBeInstanceOf(ZedToken);
            expect(result.revision.toString()).toBe(text ?? "");
            expect(Object.isFrozen(result)).toBe(true);
            expect(Reflect.set(result, "revision", new ZedToken("other"))).toBe(false);
        } finally { client.close(); }
    });
});
