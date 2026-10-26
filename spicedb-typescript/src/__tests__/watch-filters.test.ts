import { describe, it, expect, vi } from "vitest";
import { SpiceDBClient, TransportSecurity, SubjectRelationKind, InvalidArgumentError, type RelationshipFilter } from "../index.js";

function setup() {
    const client = new SpiceDBClient({ endpoint: "localhost:1", token: "secret", transportSecurity: TransportSecurity.Plaintext });
    const watch = vi.spyOn(client.raw().watch, "watch").mockImplementation(async function* () {});
    return { client, watch };
}
describe("watch relationship filters", () => {
    it("snapshots multiple full filters and preserves direct versus named subject relations", async () => {
        const { client, watch } = setup();
        const first = { resourceType: "document", resourceId: "one", relation: "viewer", subject: { subjectType: "user", subjectId: "alice", relation: { kind: SubjectRelationKind.Direct as const } } };
        const filters: RelationshipFilter[] = [first, { resourceType: "folder", resourceIdPrefix: "team/", relation: "reader", subject: { subjectType: "group", relation: { kind: SubjectRelationKind.Named, relation: "member" } } }];
        try {
            const events = client.watch({ relationshipFilters: filters });
            first.resourceId = "changed";
            first.subject.subjectId = "changed";
            filters.length = 0;
            await events.next();
            const request = watch.mock.calls[0][0];
            expect(request.optionalObjectTypes).toEqual([]);
            expect(request.optionalRelationshipFilters).toHaveLength(2);
            expect(request.optionalRelationshipFilters?.[0]).toMatchObject({ resourceType: "document", optionalResourceId: "one", optionalRelation: "viewer", optionalSubjectFilter: { subjectType: "user", optionalSubjectId: "alice", optionalRelation: { relation: "" } } });
            expect(request.optionalRelationshipFilters?.[1]).toMatchObject({ resourceType: "folder", optionalResourceIdPrefix: "team/", optionalRelation: "reader", optionalSubjectFilter: { subjectType: "group", optionalRelation: { relation: "member" } } });
        } finally { client.close(); }
    });
    it("rejects object types combined with relationship filters before transport", () => {
        const { client, watch } = setup();
        try {
            expect(() => client.watch({ objectTypes: ["document"], relationshipFilters: [{ resourceType: "document" }] })).toThrow(InvalidArgumentError);
            expect(watch).not.toHaveBeenCalled();
        } finally { client.close(); }
    });
    it("allows an empty filter list to watch all relationship types", async () => {
        const { client, watch } = setup();
        try {
            await client.watch({ relationshipFilters: [] }).next();
            expect(watch.mock.calls[0][0].optionalRelationshipFilters).toEqual([]);
            expect(watch.mock.calls[0][0].optionalObjectTypes).toEqual([]);
        } finally { client.close(); }
    });
});
