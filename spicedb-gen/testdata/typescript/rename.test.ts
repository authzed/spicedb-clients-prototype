// renamed.ts is generated from rename.zed with --typescript.rename=subject=SubjectDef:
// `definition subject` would otherwise produce SubjectRef, which the generated
// module imports from the native client. Renames change identifiers only.
import { describe, it, expect } from "vitest";
import { relationshipToString } from "@spicedb/client";
import { Resource, SubjectDef, ResourceRelations, SubjectDefRelations } from "./renamed.js";

describe("--typescript.rename", () => {
    it("renames identifiers but never the schema names sent to SpiceDB", () => {
        const rel = Resource("r1").viewer(SubjectDef("s1")).relationship();
        expect(relationshipToString(rel)).toBe("resource:r1#viewer@subject:s1");
        const caveated = Resource("r1").viewer(SubjectDef("s1").withCustomFn({ request: { k: 1 } })).relationship();
        expect(caveated.caveat).toEqual({ name: "custom_fn", context: { request: { k: 1 } } });
        expect(Resource.view.toString()).toBe("resource#view");
        expect(ResourceRelations.viewer).toBe("viewer");
        expect(Object.keys(SubjectDefRelations)).toEqual([]);
    });
});
