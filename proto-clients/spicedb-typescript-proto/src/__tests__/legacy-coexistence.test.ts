// Root DESIGN.md, "Be the single copy of the protocol bindings in any process":
// each proto tier ships a test that loads its legacy library and itself in one
// process. Go, Python, Ruby and the JVM have process-global descriptor
// registries, so a second generated copy of authzed.api.* panics or misbinds.
// protobuf-es v2 has no global registry (descriptors are values; registries
// are explicit), and @authzed/authzed-node's protobuf-ts runtime has none
// either, so both load side by side. This test proves that, and that the two
// bindings agree on the wire, so a migration can move one call at a time.
import { describe, it, expect } from "vitest";
import { create, toBinary, fromBinary } from "@bufbuild/protobuf";
import { v1 as legacy } from "@authzed/authzed-node";
import { CheckPermissionRequestSchema, RelationshipSchema, createSpiceDBClient } from "../index.js";

describe("coexistence with @authzed/authzed-node", () => {
    it("loads both bindings in one process", () => {
        const ours = create(CheckPermissionRequestSchema, { permission: "view" });
        const theirs = legacy.CheckPermissionRequest.create({ permission: "view" });
        expect(ours.$typeName).toBe("authzed.api.v1.CheckPermissionRequest");
        expect(legacy.CheckPermissionRequest.typeName).toBe("authzed.api.v1.CheckPermissionRequest");
        expect(theirs.permission).toBe("view");
        // Both client stacks can be constructed in the same process too.
        createSpiceDBClient("localhost:1", "t", { insecure: true }).close();
        legacy.NewClient("t", "localhost:1", legacy.ClientSecurity.INSECURE_LOCALHOST_ALLOWED).close();
    });

    it("exchanges messages over the wire format in both directions", () => {
        const legacyRel = legacy.Relationship.create({
            resource: { objectType: "document", objectId: "readme" },
            relation: "viewer",
            subject: { object: { objectType: "team", objectId: "eng" }, optionalRelation: "member" },
            optionalCaveat: { caveatName: "on_network", context: legacy.PbStruct.fromJson({ cidr: "10.0.0.0/8" }) },
        });
        const ours = fromBinary(RelationshipSchema, legacy.Relationship.toBinary(legacyRel));
        expect(ours.subject?.optionalRelation).toBe("member");
        expect(ours.optionalCaveat?.context).toEqual({ cidr: "10.0.0.0/8" });
        const back = legacy.Relationship.fromBinary(toBinary(RelationshipSchema, ours));
        expect(legacy.Relationship.equals(back, legacyRel)).toBe(true);
    });
});
