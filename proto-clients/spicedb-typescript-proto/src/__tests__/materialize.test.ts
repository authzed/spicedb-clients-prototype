import { describe, expect, it } from "vitest";
import { createRouterTransport } from "@connectrpc/connect";
import { SpiceDBProtoClient } from "../client.js";
import { RoaringLookupResourcesService } from "../gen/authzed/api/materialize/v0/roaringlookupresources_pb.js";

describe("materialize service", () => {
  it("uses the supplied transport and preserves response fields", async () => {
    const transport = createRouterTransport((router) => {
      router.service(RoaringLookupResourcesService, {
        experimentalRoaringLookupResources(request) {
          expect(request.resourceObjectType).toBe("document");
          return {
            bitmap: new Uint8Array([1, 2, 3]),
            cardinality: 2n,
            atRevision: { token: "revision" },
          };
        },
      });
    });
    const client = new SpiceDBProtoClient(transport);
    const result = await client.materialize.experimentalRoaringLookupResources({
      resourceObjectType: "document",
      permission: "view",
      subject: { object: { objectType: "user", objectId: "alice" } },
    });
    expect(result.bitmap).toEqual(new Uint8Array([1, 2, 3]));
    expect(result.cardinality).toBe(2n);
    expect(result.atRevision?.token).toBe("revision");
    client.close();
  });
});
