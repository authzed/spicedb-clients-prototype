/**
 * @internal A light, name-level schema differ for {@link InMemorySpiceDB.diffSchema}.
 *
 * It reads definitions (relations with their subject types, permissions with
 * their expressions), caveats (parameters and expression) and ignores
 * comments and `use` directives. It does not compile or validate the schema,
 * and it reports no doc-comment changes: comments are stripped before
 * comparing.
 */
import { InvalidArgumentError } from "./errors.js";
import type { SchemaCaveat, SchemaCaveatParameter, SchemaDefinition, SchemaDiff, SchemaRelation, SchemaTypeReference } from "./types.js";

interface ParsedDefinition { readonly name: string; readonly relations: Map<string, string[]>; readonly permissions: Map<string, string> }
interface ParsedCaveat { readonly name: string; readonly parameters: Map<string, string>; readonly expression: string }
interface ParsedSchema { readonly definitions: Map<string, ParsedDefinition>; readonly caveats: Map<string, ParsedCaveat> }

const NAME = String.raw`[A-Za-z_][\w/]*`;

function stripComments(text: string): string {
    return text.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/\/\/[^\n]*/g, " ");
}
function collapse(text: string): string { return text.replace(/\s+/g, " ").trim(); }
/** The index just past the bracket closing the one at `open`. */
function closing(text: string, open: number, left: string, right: string): number {
    let depth = 0;
    for (let i = open; i < text.length; i++) {
        if (text[i] === left)
            depth++;
        else if (text[i] === right && --depth === 0)
            return i + 1;
    }
    throw new InvalidArgumentError(`schema: unbalanced "${left}"`);
}

/** Parses schema text into its named parts; unparseable text fails with `InvalidArgumentError`, as the server's parse would. */
export function parseSchema(source: string): ParsedSchema {
    const text = stripComments(source);
    const definitions = new Map<string, ParsedDefinition>();
    const caveats = new Map<string, ParsedCaveat>();
    let at = 0;
    const header = new RegExp(String.raw`\s*(?:(use)\s+\w+|(definition|caveat)\s+(${NAME})\s*)`, "y");
    for (;;) {
        while (at < text.length && /\s/.test(text[at]!))
            at++;
        if (at >= text.length)
            break;
        header.lastIndex = at;
        const match = header.exec(text);
        if (!match)
            throw new InvalidArgumentError(`schema: cannot parse near "${collapse(text.slice(at, at + 30))}"`);
        at = header.lastIndex;
        if (match[1] === "use")
            continue;
        const name = match[3]!;
        if (match[2] === "definition") {
            if (text[at] !== "{")
                throw new InvalidArgumentError(`schema: definition ${name} has no body`);
            const end = closing(text, at, "{", "}");
            definitions.set(name, parseDefinition(name, text.slice(at + 1, end - 1)));
            at = end;
        }
        else {
            if (text[at] !== "(")
                throw new InvalidArgumentError(`schema: caveat ${name} has no parameters`);
            const paramsEnd = closing(text, at, "(", ")");
            const parameters = new Map<string, string>();
            for (const param of text.slice(at + 1, paramsEnd - 1).split(",").map(collapse).filter(Boolean)) {
                const space = param.indexOf(" ");
                if (space < 0)
                    throw new InvalidArgumentError(`schema: caveat ${name} parameter "${param}" has no type`);
                parameters.set(param.slice(0, space), param.slice(space + 1).replace(/\s+/g, ""));
            }
            at = paramsEnd;
            while (/\s/.test(text[at] ?? ""))
                at++;
            if (text[at] !== "{")
                throw new InvalidArgumentError(`schema: caveat ${name} has no expression`);
            const end = closing(text, at, "{", "}");
            caveats.set(name, { name, parameters, expression: collapse(text.slice(at + 1, end - 1)) });
            at = end;
        }
    }
    return { definitions, caveats };
}

function parseDefinition(name: string, body: string): ParsedDefinition {
    const relations = new Map<string, string[]>();
    const permissions = new Map<string, string>();
    const statement = new RegExp(String.raw`\b(relation|permission)\s+(\w+)\s*([:=])`, "g");
    const matches = [...body.matchAll(statement)];
    const leading = collapse(matches.length ? body.slice(0, matches[0]!.index) : body);
    if (leading.replace(/;/g, ""))
        throw new InvalidArgumentError(`schema: cannot parse definition ${name} near "${leading.slice(0, 30)}"`);
    matches.forEach((m, i) => {
        const value = collapse(body.slice(m.index! + m[0].length, i + 1 < matches.length ? matches[i + 1]!.index : body.length)).replace(/;$/, "").trim();
        if (m[1] === "relation" && m[3] === ":")
            relations.set(m[2]!, value.split("|").map(collapse).filter(Boolean));
        else if (m[1] === "permission" && m[3] === "=")
            permissions.set(m[2]!, value);
        else
            throw new InvalidArgumentError(`schema: malformed ${m[1]} ${m[2]} in definition ${name}`);
    });
    return { name, relations, permissions };
}

function typeReference(text: string): SchemaTypeReference {
    const m = new RegExp(String.raw`^(${NAME})(?:#(\w+)|:(\*))?(?:\s+with\s+(.+))?$`).exec(text);
    if (!m)
        throw new InvalidArgumentError(`schema: cannot parse subject type "${text}"`);
    const caveat = m[4]?.split(/\s+and\s+/).find(trait => trait !== "expiration");
    return {
        subjectDefinitionName: m[1]!,
        ...(caveat ? { caveatName: caveat } : {}),
        type: m[2] !== undefined ? { kind: "relation", relation: m[2] } : m[3] !== undefined ? { kind: "wildcard", wildcard: true } : { kind: "terminal", terminal: true },
    };
}
function relation(definition: string, name: string, types: readonly string[]): SchemaRelation {
    return { name, comment: "", parentDefinitionName: definition, subjectTypes: types.map(typeReference) };
}
function definition(d: ParsedDefinition): SchemaDefinition {
    return {
        name: d.name, comment: "",
        relations: [...d.relations].map(([name, types]) => relation(d.name, name, types)),
        permissions: [...d.permissions.keys()].map(name => ({ name, comment: "", parentDefinitionName: d.name })),
    };
}
function parameter(caveat: string, name: string, type: string): SchemaCaveatParameter { return { name, type, parentCaveatName: caveat }; }
function caveat(c: ParsedCaveat): SchemaCaveat {
    return { name: c.name, comment: "", expression: c.expression, parameters: [...c.parameters].map(([name, type]) => parameter(c.name, name, type)) };
}
const empty = { definitionName: "", relationName: "", permissionName: "", caveatName: "" };

/**
 * The changes from `existing` to `comparison`, as SpiceDB's DiffSchema reports
 * them: "added" is in the comparison schema only. Definitions and caveats are
 * compared by name, then their relations (and subject types), permissions
 * (and expressions), parameters (and types) and caveat expressions.
 */
export function diffSchemas(existing: string, comparison: string): SchemaDiff[] {
    const before = parseSchema(existing);
    const after = parseSchema(comparison);
    const diffs: SchemaDiff[] = [];
    for (const [name, d] of before.definitions)
        if (!after.definitions.has(name))
            diffs.push({ ...empty, kind: "definition_removed", definitionName: name, definition: definition(d) });
    for (const [name, d] of after.definitions) {
        const old = before.definitions.get(name);
        if (!old) {
            diffs.push({ ...empty, kind: "definition_added", definitionName: name, definition: definition(d) });
            continue;
        }
        for (const [rel, types] of old.relations)
            if (!d.relations.has(rel))
                diffs.push({ ...empty, kind: "relation_removed", definitionName: name, relationName: rel, relation: relation(name, rel, types) });
        for (const [rel, types] of d.relations) {
            const oldTypes = old.relations.get(rel);
            if (!oldTypes) {
                diffs.push({ ...empty, kind: "relation_added", definitionName: name, relationName: rel, relation: relation(name, rel, types) });
                continue;
            }
            for (const type of oldTypes)
                if (!types.includes(type))
                    diffs.push({ ...empty, kind: "relation_subject_type_removed", definitionName: name, relationName: rel, relation: relation(name, rel, types), changedSubjectType: typeReference(type) });
            for (const type of types)
                if (!oldTypes.includes(type))
                    diffs.push({ ...empty, kind: "relation_subject_type_added", definitionName: name, relationName: rel, relation: relation(name, rel, types), changedSubjectType: typeReference(type) });
        }
        const permission = (perm: string) => ({ name: perm, comment: "", parentDefinitionName: name });
        for (const perm of old.permissions.keys())
            if (!d.permissions.has(perm))
                diffs.push({ ...empty, kind: "permission_removed", definitionName: name, permissionName: perm, permission: permission(perm) });
        for (const [perm, expression] of d.permissions) {
            const oldExpression = old.permissions.get(perm);
            if (oldExpression === undefined)
                diffs.push({ ...empty, kind: "permission_added", definitionName: name, permissionName: perm, permission: permission(perm) });
            else if (oldExpression !== expression)
                diffs.push({ ...empty, kind: "permission_expr_changed", definitionName: name, permissionName: perm, permission: permission(perm) });
        }
    }
    for (const [name, c] of before.caveats)
        if (!after.caveats.has(name))
            diffs.push({ ...empty, kind: "caveat_removed", caveatName: name, caveat: caveat(c) });
    for (const [name, c] of after.caveats) {
        const old = before.caveats.get(name);
        if (!old) {
            diffs.push({ ...empty, kind: "caveat_added", caveatName: name, caveat: caveat(c) });
            continue;
        }
        for (const [param, type] of old.parameters)
            if (!c.parameters.has(param))
                diffs.push({ ...empty, kind: "caveat_parameter_removed", caveatName: name, parameter: parameter(name, param, type) });
        for (const [param, type] of c.parameters) {
            const oldType = old.parameters.get(param);
            if (oldType === undefined)
                diffs.push({ ...empty, kind: "caveat_parameter_added", caveatName: name, parameter: parameter(name, param, type) });
            else if (oldType !== type)
                diffs.push({ ...empty, kind: "caveat_parameter_type_changed", caveatName: name, parameter: parameter(name, param, type), previousType: oldType });
        }
        if (old.expression !== c.expression)
            diffs.push({ ...empty, kind: "caveat_expr_changed", caveatName: name, caveat: caveat(c) });
    }
    return diffs;
}
