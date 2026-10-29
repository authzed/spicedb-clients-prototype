// A shared API module that uses the names-only output. `mage
// typeScriptTypeErrors` copies this directory somewhere with no node_modules
// and type-checks it there, so any import of @spicedb/* fails to resolve.
import {
    Definitions,
    DefinitionNames,
    DocumentPermissions,
    DocumentPermissionNames,
    OrganizationRelations,
    SchemaText,
    type DefinitionName,
    type DocumentPermissionName,
} from "./names.gen";

export interface ResourceAction {
    resourceType: DefinitionName;
    permission: DocumentPermissionName;
}

export const viewDocument: ResourceAction = {
    resourceType: Definitions.document,
    permission: DocumentPermissions.view,
};

export const adminRelation: "admin" = OrganizationRelations.admin;
export const allDefinitions: readonly string[] = DefinitionNames;
export const documentPermissions: readonly DocumentPermissionName[] = DocumentPermissionNames;
export const schema: string = SchemaText;

// @ts-expect-error the literal union admits only the schema's permission names
export const notAPermission: DocumentPermissionName = "owner";
