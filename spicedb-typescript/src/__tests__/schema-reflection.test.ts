import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  PermissionRelationshipTreeSchema,
  AlgebraicSubjectSetSchema,
  AlgebraicSubjectSet_Operation,
  DirectSubjectSetSchema,
  SubjectReferenceSchema,
  ObjectReferenceSchema,
  ReflectionDefinitionSchema,
  ReflectionRelationSchema,
  ReflectionPermissionSchema,
  ReflectionCaveatSchema,
  ReflectionCaveatParameterSchema,
  ReflectionRelationReferenceSchema,
  ReflectionSchemaDiffSchema,
  ReflectionRelationSubjectTypeChangeSchema,
} from "@spicedb/proto";
import {
  fromProtoPermissionTree,
  fromProtoSchemaDefinition,
  fromProtoSchemaCaveat,
  fromProtoRelationReference,
  fromProtoSchemaDiff,
} from "../types.js";

describe("fromProtoPermissionTree", () => {
  it("maps a union root with a leaf and a nested intermediate child", () => {
    const leafNode = create(PermissionRelationshipTreeSchema, {
      expandedObject: create(ObjectReferenceSchema, {
        objectType: "document",
        objectId: "doc1",
      }),
      expandedRelation: "viewer",
      treeType: {
        case: "leaf",
        value: create(DirectSubjectSetSchema, {
          subjects: [
            create(SubjectReferenceSchema, {
              object: create(ObjectReferenceSchema, {
                objectType: "user",
                objectId: "alice",
              }),
              optionalRelation: "",
            }),
            create(SubjectReferenceSchema, {
              object: create(ObjectReferenceSchema, {
                objectType: "group",
                objectId: "eng",
              }),
              optionalRelation: "member",
            }),
          ],
        }),
      },
    });

    const nestedIntermediate = create(PermissionRelationshipTreeSchema, {
      expandedObject: create(ObjectReferenceSchema, {
        objectType: "document",
        objectId: "doc1",
      }),
      expandedRelation: "editor",
      treeType: {
        case: "intermediate",
        value: create(AlgebraicSubjectSetSchema, {
          operation: AlgebraicSubjectSet_Operation.EXCLUSION,
          children: [],
        }),
      },
    });

    const root = create(PermissionRelationshipTreeSchema, {
      expandedObject: create(ObjectReferenceSchema, {
        objectType: "document",
        objectId: "doc1",
      }),
      expandedRelation: "viewer",
      treeType: {
        case: "intermediate",
        value: create(AlgebraicSubjectSetSchema, {
          operation: AlgebraicSubjectSet_Operation.UNION,
          children: [leafNode, nestedIntermediate],
        }),
      },
    });

    const tree = fromProtoPermissionTree(root);

    expect(tree.expandedObject).toEqual({
      objectType: "document",
      objectId: "doc1",
    });
    expect(tree.expandedRelation).toBe("viewer");
    expect(tree.leaf).toBeUndefined();
    expect(tree.intermediate?.operation).toBe("union");
    expect(tree.intermediate?.children).toHaveLength(2);

    const [leaf, nested] = tree.intermediate!.children;
    expect(leaf.intermediate).toBeUndefined();
    expect(leaf.leaf?.subjects).toEqual([
      { subjectType: "user", subjectId: "alice", optionalRelation: "" },
      { subjectType: "group", subjectId: "eng", optionalRelation: "member" },
    ]);

    expect(nested.leaf).toBeUndefined();
    expect(nested.intermediate?.operation).toBe("exclusion");
    expect(nested.intermediate?.children).toEqual([]);
  });

  it("maps undefined input to a zero-value tree (mirrors Go's nil handling)", () => {
    const tree = fromProtoPermissionTree(undefined);
    expect(tree).toEqual({
      expandedObject: { objectType: "", objectId: "" },
      expandedRelation: "",
    });
    expect(tree.intermediate).toBeUndefined();
    expect(tree.leaf).toBeUndefined();
  });
});

describe("fromProtoSchemaDefinition", () => {
  it("maps a definition with a relation and a permission", () => {
    const def = create(ReflectionDefinitionSchema, {
      name: "document",
      comment: "// a document",
      relations: [
        create(ReflectionRelationSchema, {
          name: "viewer",
          comment: "// viewers",
          parentDefinitionName: "document",
        }),
      ],
      permissions: [
        create(ReflectionPermissionSchema, {
          name: "view",
          comment: "// can view",
          parentDefinitionName: "document",
        }),
      ],
    });

    expect(fromProtoSchemaDefinition(def)).toEqual({
      name: "document",
      comment: "// a document",
      relations: [
        { name: "viewer", comment: "// viewers", parentDefinitionName: "document" },
      ],
      permissions: [
        { name: "view", comment: "// can view", parentDefinitionName: "document" },
      ],
    });
  });
});

describe("fromProtoSchemaCaveat", () => {
  it("maps a caveat with a parameter", () => {
    const cav = create(ReflectionCaveatSchema, {
      name: "is_in_region",
      comment: "// region check",
      expression: "region == expected_region",
      parameters: [
        create(ReflectionCaveatParameterSchema, {
          name: "expected_region",
          type: "string",
          parentCaveatName: "is_in_region",
        }),
      ],
    });

    expect(fromProtoSchemaCaveat(cav)).toEqual({
      name: "is_in_region",
      comment: "// region check",
      expression: "region == expected_region",
      parameters: [
        { name: "expected_region", type: "string", parentCaveatName: "is_in_region" },
      ],
    });
  });
});

describe("fromProtoRelationReference", () => {
  it("maps a list of relation references", () => {
    const refs = [
      create(ReflectionRelationReferenceSchema, {
        definitionName: "document",
        relationName: "view",
        isPermission: true,
      }),
      create(ReflectionRelationReferenceSchema, {
        definitionName: "document",
        relationName: "viewer",
        isPermission: false,
      }),
    ];

    expect(refs.map(fromProtoRelationReference)).toEqual([
      { definitionName: "document", relationName: "view", isPermission: true },
      { definitionName: "document", relationName: "viewer", isPermission: false },
    ]);
  });
});

describe("fromProtoSchemaDiff", () => {
  it("maps a definition_added diff", () => {
    const d = create(ReflectionSchemaDiffSchema, {
      diff: {
        case: "definitionAdded",
        value: create(ReflectionDefinitionSchema, { name: "document" }),
      },
    });
    expect(fromProtoSchemaDiff(d)).toEqual({
      kind: "definition_added",
      definitionName: "document",
      relationName: "",
      permissionName: "",
      caveatName: "",
    });
  });

  it("maps a relation_subject_type_added diff (nested .relation field)", () => {
    const d = create(ReflectionSchemaDiffSchema, {
      diff: {
        case: "relationSubjectTypeAdded",
        value: create(ReflectionRelationSubjectTypeChangeSchema, {
          relation: create(ReflectionRelationSchema, {
            name: "viewer",
            parentDefinitionName: "document",
          }),
        }),
      },
    });
    expect(fromProtoSchemaDiff(d)).toEqual({
      kind: "relation_subject_type_added",
      definitionName: "document",
      relationName: "viewer",
      permissionName: "",
      caveatName: "",
    });
  });

  it("maps a permission_expr_changed diff", () => {
    const d = create(ReflectionSchemaDiffSchema, {
      diff: {
        case: "permissionExprChanged",
        value: create(ReflectionPermissionSchema, {
          name: "view",
          parentDefinitionName: "document",
        }),
      },
    });
    expect(fromProtoSchemaDiff(d)).toEqual({
      kind: "permission_expr_changed",
      definitionName: "document",
      relationName: "",
      permissionName: "view",
      caveatName: "",
    });
  });

  it("maps a caveat_parameter_added diff (flat parentCaveatName field)", () => {
    const d = create(ReflectionSchemaDiffSchema, {
      diff: {
        case: "caveatParameterAdded",
        value: create(ReflectionCaveatParameterSchema, {
          name: "expected_region",
          parentCaveatName: "is_in_region",
        }),
      },
    });
    expect(fromProtoSchemaDiff(d)).toEqual({
      kind: "caveat_parameter_added",
      definitionName: "",
      relationName: "",
      permissionName: "",
      caveatName: "is_in_region",
    });
  });

  it("maps an unset diff to kind 'unknown'", () => {
    const d = create(ReflectionSchemaDiffSchema, {});
    expect(fromProtoSchemaDiff(d)).toEqual({
      kind: "unknown",
      definitionName: "",
      relationName: "",
      permissionName: "",
      caveatName: "",
    });
  });
});
