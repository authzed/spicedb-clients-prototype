import { it, expect, vi } from "vitest";
import { SpiceDBClient, TransportSecurity, ZedToken, type WriteResult } from "@spicedb/client";
import { TypedClient, Document, User } from "./permissions";

it("returns the native WriteResult unchanged from every typed write helper", async () => {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
    const typed = new TypedClient(client);
    const written: WriteResult = Object.freeze({ revision: new ZedToken("opaque/revision") });
    const write = vi.spyOn(client, "write").mockResolvedValue(written);
    const relationship = Document("readme").viewer(User("alice"));
    try {
        expect(await typed.touch([relationship])).toBe(written);
        expect(await typed.create([relationship])).toBe(written);
        expect(await typed.delete([relationship])).toBe(written);
        expect(write).toHaveBeenCalledTimes(3);
    } finally { typed.close(); }
});
