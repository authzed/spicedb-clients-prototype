import { describe, it, expect } from "vitest";
import { SpiceDBClient, createSpiceDBClient } from "../client.js";

describe("createSpiceDBClient", () => {
  it("returns a SpiceDBClient instance", () => {
    const client = createSpiceDBClient("localhost:50051", "testtoken", {
      insecure: true,
    });
    expect(client).toBeInstanceOf(SpiceDBClient);
  });
});

describe("SpiceDBClient", () => {
  it("can be constructed with options object", () => {
    const client = new SpiceDBClient({
      endpoint: "localhost:50051",
      token: "testtoken",
      insecure: true,
    });
    expect(client).toBeInstanceOf(SpiceDBClient);
  });

  it("has all expected methods", () => {
    const client = createSpiceDBClient("localhost:50051", "testtoken", {
      insecure: true,
    });
    expect(typeof client.checkPermission).toBe("function");
    expect(typeof client.checkPermissions).toBe("function");
    expect(typeof client.checkAny).toBe("function");
    expect(typeof client.checkAll).toBe("function");
    expect(typeof client.readRelationships).toBe("function");
    expect(typeof client.write).toBe("function");
    expect(typeof client.deleteRelationships).toBe("function");
    expect(typeof client.lookupResources).toBe("function");
    expect(typeof client.lookupSubjects).toBe("function");
    expect(typeof client.exportBulkRelationships).toBe("function");
    expect(typeof client.readSchema).toBe("function");
    expect(typeof client.writeSchema).toBe("function");
    expect(typeof client.reflectSchema).toBe("function");
    expect(typeof client.computablePermissions).toBe("function");
    expect(typeof client.dependentRelations).toBe("function");
    expect(typeof client.diffSchema).toBe("function");
    expect(typeof client.expandPermissionTree).toBe("function");
    expect(typeof client.importBulkRelationships).toBe("function");
    expect(typeof client.watch).toBe("function");
    // Experimental (prefixed to reserve non-prefixed names for promotion)
    expect(typeof client.experimentalRegisterRelationshipCounter).toBe("function");
    expect(typeof client.experimentalCountRelationships).toBe("function");
    expect(typeof client.experimentalUnregisterRelationshipCounter).toBe("function");
  });
});
