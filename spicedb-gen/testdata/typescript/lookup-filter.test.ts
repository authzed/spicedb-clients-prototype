import { expect, it, vi } from "vitest";
import { SpiceDBClient, TransportSecurity, LookupPermissionshipFilter, ZedToken, full } from "@spicedb/client";
import { TypedClient, Document, User } from "./permissions";

it("forwards the typed lookup filter and cancellation options without changing the iterator", async () => {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
    const typed = new TypedClient(client);
    const signal = new AbortController().signal;
    const iterator = (async function* () { yield { resourceId: "readme", permissionship: "hasPermission" as const, lookedUpAt: new ZedToken("revision") }; })();
    const lookup = vi.spyOn(client, "lookupResources").mockReturnValue(iterator);
    try {
        const result = typed.lookupResources(full(), Document.view, User("alice"), { permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly, signal });
        expect(result).toBe(iterator);
        expect(lookup.mock.calls[0][0].permissionshipFilter).toBe(LookupPermissionshipFilter.HasPermissionOnly);
        expect(lookup.mock.calls[0][1]?.signal).toBe(signal);
        expect((await result.next()).value?.resourceId).toBe("readme");
    } finally { typed.close(); }
});
