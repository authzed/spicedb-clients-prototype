import { serverToken, tokenString } from "./token.js";
import { Tracing, LookupDebug, LookupPermissionshipFilter, Wildcards, WatchContent, TransportSecurity, RemoteCredentials, mode } from "./modes.js";
import { create, clone, equals, type JsonObject } from "@bufbuild/protobuf";
import { createSpiceDBClient as createProtoClient, type SpiceDBProtoClient, type TlsOptions, CheckBulkPermissionsRequestItemSchema, CheckBulkPermissionsRequestSchema, CheckPermissionRequestSchema, ReadRelationshipsRequestSchema, WriteRelationshipsRequestSchema, LookupResourcesRequestSchema, LookupSubjectsRequestSchema, ExpandPermissionTreeRequestSchema, ExportBulkRelationshipsRequestSchema, ImportBulkRelationshipsRequestSchema, ReadSchemaRequestSchema, WriteSchemaRequestSchema, ReflectSchemaRequestSchema, ReflectionSchemaFilterSchema, ComputablePermissionsRequestSchema, DependentRelationsRequestSchema, DiffSchemaRequestSchema, ExperimentalRegisterRelationshipCounterRequestSchema, ExperimentalCountRelationshipsRequestSchema, ExperimentalUnregisterRelationshipCounterRequestSchema, WatchRequestSchema, WatchKind, LookupSubjectsRequest_WildcardOption, ZedTokenSchema, CursorSchema, RelationshipUpdate_Operation, DeleteRelationshipsResponse_DeletionProgress, DeleteRelationshipsRequestSchema, } from "@spicedb/proto";
import { ExperimentalCountRelationshipsByFilterRequestSchema, MaterializePermissionSetsProtocol as PermissionSets } from "@spicedb/proto";
import type { WatchPermissionSetsRequest, PermissionSetEvent, LookupPermissionSetsRequest, LookupPermissionSet, DownloadPermissionSetsRequest, DownloadPermissionSetsResult, PermissionSetCursor, PermissionSetChange, PermissionSetReference } from "./types.js";
import { Consistency } from "./consistency.js";
import { type Relationship, type RelationshipFilter, type CheckRequest, type CheckOptions, type CheckResult, type CallOptions, type DeleteOptions, type DeleteAllOptions, type DeleteResult, type WriteResult, type ReadRelationshipsRequest, type LookupResourcesRequest, type LookupSubjectsRequest, type LookupResource, type LookupSubject, type ExpandPermissionTreeRequest, type ExpandResult, type ExportRelationshipsRequest, type WatchRequest, type WatchEvent, type WatchChange, type ReflectSchemaRequest, type ReflectSchemaResult, type ComputablePermissionsRequest, type DependentRelationsRequest, type RelationsResult, type DiffSchemaRequest, type SchemaDiffResult, type SchemaResult, type RelationshipCountResult, type RoaringLookupResourcesRequest, type RoaringLookupResourcesResult, type ZedToken, Transaction, toProtoRelationship, fromProtoRelationship, toProtoRelationshipFilter, toProtoDeleteRelationshipsRequest, fromProtoPermissionTree, fromProtoSchemaDefinition, fromProtoSchemaCaveat, fromProtoRelationReference, fromProtoSchemaDiff, fromProtoLookupResource, fromProtoLookupSubject, checkResultFromProto, checkResultFromBulkItem, mergeCheckContext, immutableSnapshot, jsonSnapshot, protoSubject, positiveLimit, } from "./types.js";
import { InvalidArgumentError, SpiceDBError, toSpiceDBErrorFromStatus, isTransientError } from "./errors.js";
import { Operation, withDeleteProgress } from "./operation.js";
export type { SpiceDBProtoClient, TlsOptions };
export interface SpiceDBClientOptions {
    readonly endpoint: string;
    readonly token: string;
    readonly transportSecurity?: TransportSecurity;
    readonly remoteCredentials?: RemoteCredentials;
    readonly tls?: TlsOptions;
    readonly headers?: Readonly<Record<string, string>>;
    readonly maxRetries?: number;
    readonly defaultTimeoutMs?: number;
}
/** Reusable authenticated client. Unary calls share a total operation budget; streams have no default lifetime limit. */
export class SpiceDBClient {
    private readonly proto: SpiceDBProtoClient;
    private readonly maxRetries: number;
    private readonly defaultTimeoutMs: number;
    private readonly operations = new Set<Operation>();
    private closed = false;
    constructor(options: SpiceDBClientOptions) {
        this.defaultTimeoutMs = options.defaultTimeoutMs ?? 30000;
        if (!Number.isFinite(this.defaultTimeoutMs) || this.defaultTimeoutMs <= 0)
            throw new InvalidArgumentError("defaultTimeoutMs must be finite and positive");
        this.maxRetries = options.maxRetries ?? 3;
        if (!Number.isInteger(this.maxRetries) || this.maxRetries < 0 || this.maxRetries > 3)
            throw new InvalidArgumentError("maxRetries must be an integer from 0 through 3");
        try {
            this.proto = createProtoClient(options.endpoint, options.token, { insecure: mode(options.transportSecurity, TransportSecurity, TransportSecurity.Tls, "transportSecurity") === TransportSecurity.Plaintext, allowInsecureRemoteCredentials: mode(options.remoteCredentials, RemoteCredentials, RemoteCredentials.LoopbackOnly, "remoteCredentials") === RemoteCredentials.AllowPlaintext, tls: options.tls, headers: options.headers ? { ...options.headers } : undefined });
        }
        catch (error) {
            throw new InvalidArgumentError(error instanceof Error ? error.message.replace(/allowInsecureRemoteCredentials/g, "remoteCredentials: RemoteCredentials.AllowPlaintext").replace(/insecure: true/g, "transportSecurity: TransportSecurity.Plaintext") : String(error), { cause: error });
        }
    }
    /** Secondary authenticated protocol accessor; shares ownership and bypasses idiomatic retry, errors and deadlines. */
    raw(): SpiceDBProtoClient { return this.proto; }
    close(): void {
        if (this.closed)
            return;
        this.closed = true;
        for (const op of this.operations)
            op.cancel();
        this.proto.close();
    }
    private backoffMs(attempt: number): number { return Math.random() * 100 * 2 ** attempt; }
    private effectiveTimeoutMs(timeoutMs?: number): number { return timeoutMs ?? this.defaultTimeoutMs; }
    private operation(options?: CallOptions, streaming = false): Operation {
        if (this.closed)
            throw new SpiceDBError("Client is closed");
        const op = new Operation(options, streaming ? undefined : this.effectiveTimeoutMs(options?.timeoutMs), () => this.operations.delete(op));
        this.operations.add(op);
        return op;
    }
    private consistency(value: Consistency) {
        if (!(value instanceof Consistency))
            throw new InvalidArgumentError("consistency is required");
        return value._toProto();
    }
    private async retry<T>(op: Operation, fn: () => Promise<T>, read: boolean): Promise<T> {
        for (let attempt = 0;; attempt++) {
            try {
                op.check();
                return await op.race(fn());
            }
            catch (error) {
                const mapped = op.map(error);
                if (!read || attempt >= this.maxRetries || !isTransientError(error))
                    throw mapped;
                await op.sleep(this.backoffMs(attempt));
            }
        }
    }
    private async unary<T>(options: CallOptions | undefined, read: boolean, fn: (op: Operation) => Promise<T>): Promise<T> {
        const op = this.operation(options);
        try {
            return await this.retry(op, () => fn(op), read);
        }
        catch (error) {
            throw op.map(error);
        }
        finally {
            op.dispose();
        }
    }
    private stream<T>(options: CallOptions | undefined, factory: (op: Operation) => AsyncGenerator<T>, retry = true): AsyncIterableIterator<T> {
        const op = this.operation(options, true);
        const self = this;
        const iterator = (async function* () {
            let yielded = false;
            try {
                for (let attempt = 0;; attempt++) {
                    let current: AsyncGenerator<T> | undefined;
                    try {
                        op.check();
                        current = factory(op);
                        for (;;) {
                            const item = await op.race(current.next());
                            if (item.done)
                                return;
                            op.check();
                            yielded = true;
                            yield immutableSnapshot(item.value);
                        }
                    }
                    catch (error) {
                        if (!retry || yielded || attempt >= self.maxRetries || !isTransientError(error))
                            throw op.map(error);
                        await op.sleep(self.backoffMs(attempt));
                    }
                    finally {
                        if (current) {
                            const closing = current.return(undefined);
                            if (op.controller.signal.aborted)
                                void closing.catch(() => { });
                            else
                                await closing;
                        }
                    }
                }
            }
            catch (error) {
                throw op.map(error);
            }
            finally {
                op.dispose();
            }
        })();
        return {
            next: value => iterator.next(value),
            return: async (value) => { op.cancel(); return iterator.return(value); },
            throw: async (error) => { op.cancel(); return iterator.throw(error); },
            [Symbol.asyncIterator]() { return this; },
        };
    }
    async check(request: CheckRequest, options?: CheckOptions): Promise<CheckResult> {
        const wire = create(CheckPermissionRequestSchema, { resource: request.resource, permission: request.permission, subject: protoSubject(request.subject), consistency: this.consistency(request.consistency), context: mergeCheckContext(options?.context, request.context), withTracing: mode(options?.tracing, Tracing, Tracing.Disabled, "tracing") === Tracing.Enabled });
        return this.unary(options, true, async (op) => checkResultFromProto(await this.proto.permissions.checkPermission(wire, op.callOptions())));
    }
    async checkBulk(requests: readonly CheckRequest[], options?: CheckOptions): Promise<readonly CheckResult[]> {
        const op = this.operation(options);
        try {
            op.check();
            const key = requests[0]?.consistency;
            const items = requests.map((request, index) => {
                if (!(request.consistency instanceof Consistency) || !(key instanceof Consistency) || request.consistency._key() !== key._key())
                    throw new InvalidArgumentError(`check item ${index}: bulk consistency must be identical`);
                return create(CheckBulkPermissionsRequestItemSchema, { resource: request.resource, permission: request.permission, subject: protoSubject(request.subject), context: mergeCheckContext(options?.context, request.context) });
            });
            const tracing = mode(options?.tracing, Tracing, Tracing.Disabled, "tracing") === Tracing.Enabled;
            if (items.length === 0)
                return Object.freeze([]);
            const consistency = this.consistency(key!);
            const results: CheckResult[] = [];
            for (let offset = 0; offset < items.length; offset += 1000) {
                op.check();
                const chunk = items.slice(offset, offset + 1000);
                const resp = await this.retry(op, () => this.proto.permissions.checkBulkPermissions(create(CheckBulkPermissionsRequestSchema, { consistency, items: chunk, withTracing: tracing }), op.callOptions()), true);
                if (resp.pairs.length !== chunk.length)
                    throw new SpiceDBError(`checkBulkPermissions returned ${resp.pairs.length} pair(s) for ${chunk.length} request item(s)`);
                resp.pairs.forEach((pair, i) => {
                    if (!pair.request || !equals(CheckBulkPermissionsRequestItemSchema, pair.request, chunk[i])) {
                        const error = new SpiceDBError(`check item ${offset + i}: malformed echoed request`);
                        Object.defineProperty(error, "inputIndex", { value: offset + i, enumerable: true });
                        throw error;
                    }
                    if (pair.response.case === "error") {
                        const error = toSpiceDBErrorFromStatus(pair.response.value);
                        Object.defineProperty(error, "inputIndex", { value: offset + i, enumerable: true });
                        error.message = `check item ${offset + i}: ${error.message}`;
                        throw error;
                    }
                    if (pair.response.case !== "item")
                        throw new SpiceDBError(`check item ${offset + i}: malformed CheckBulkPermissionsPair (neither item nor error set)`);
                    results.push(checkResultFromBulkItem(pair.response.value, serverToken(resp.checkedAt?.token)));
                });
            }
            return Object.freeze(results);
        }
        catch (error) {
            throw op.map(error);
        }
        finally {
            op.dispose();
        }
    }
    async write(transaction: Transaction, options?: CallOptions): Promise<WriteResult> {
        const wire = create(WriteRelationshipsRequestSchema, transaction._build());
        return this.unary(options, false, async (op) => immutableSnapshot({ revision: serverToken((await this.proto.permissions.writeRelationships(wire, op.callOptions())).writtenAt?.token) }));
    }
    readRelationships(request: ReadRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<Relationship> {
        const size = positiveLimit(request.pageSize ?? 1000, "pageSize");
        const wire = create(ReadRelationshipsRequestSchema, { consistency: this.consistency(request.consistency), relationshipFilter: toProtoRelationshipFilter(request.filter), optionalLimit: size, optionalCursor: request.cursor === undefined ? undefined : create(CursorSchema, { token: request.cursor }) });
        const self = this;
        return this.stream(options, async function* (op) {
            for (;;) {
                op.check();
                let count = 0;
                let cursor: string | undefined;
                for await (const resp of self.proto.permissions.readRelationships(clone(ReadRelationshipsRequestSchema, wire), op.callOptions())) {
                    op.check();
                    cursor = resp.afterResultCursor?.token;
                    if (resp.relationship) {
                        count++;
                        yield fromProtoRelationship(resp.relationship);
                    }
                }
                if (count < size || !cursor)
                    return;
                if (cursor === wire.optionalCursor?.token)
                    throw new SpiceDBError("Read cursor did not advance");
                wire.optionalCursor = create(CursorSchema, { token: cursor });
            }
        });
    }
    async deleteRelationships(filter: RelationshipFilter, options: DeleteOptions): Promise<DeleteResult> {
        const wire = toProtoDeleteRelationshipsRequest(filter, options);
        return this.unary(options, false, async (op) => this.deleteResult(await this.proto.permissions.deleteRelationships(wire, op.callOptions())));
    }
    private deleteResult(resp: Awaited<ReturnType<SpiceDBProtoClient["permissions"]["deleteRelationships"]>>, previous = 0n): DeleteResult {
        return immutableSnapshot({ revision: serverToken(resp.deletedAt?.token), deletedCount: previous + resp.relationshipsDeletedCount, cursor: resp.afterResultCursor?.token, progress: resp.deletionProgress === DeleteRelationshipsResponse_DeletionProgress.COMPLETE ? "complete" : resp.deletionProgress === DeleteRelationshipsResponse_DeletionProgress.PARTIAL ? "partial" : "unspecified" });
    }
    async deleteAllRelationshipsNonAtomic(filter: RelationshipFilter, options?: DeleteAllOptions): Promise<DeleteResult> {
        const wire = toProtoDeleteRelationshipsRequest(filter, { ...options, limit: positiveLimit(options?.pageSize ?? 1000, "pageSize") });
        const op = this.operation(options);
        let progress: DeleteResult | undefined;
        try {
            for (;;) {
                op.check();
                const resp = await this.retry(op, () => this.proto.permissions.deleteRelationships(clone(DeleteRelationshipsRequestSchema, wire), op.callOptions()), false);
                progress = this.deleteResult(resp, progress?.deletedCount);
                if (progress.progress === "complete")
                    return progress;
                if (progress.progress === "unspecified")
                    throw new SpiceDBError("delete-all returned unknown deletion progress");
                wire.optionalCursor = progress.cursor ? create(CursorSchema, { token: progress.cursor }) : undefined;
            }
        }
        catch (error) {
            const mapped = op.map(error);
            throw progress ? withDeleteProgress(mapped, progress) : mapped;
        }
        finally {
            op.dispose();
        }
    }
    lookupResources(request: LookupResourcesRequest, options?: CallOptions): AsyncIterableIterator<LookupResource> {
        const filter = mode(request.permissionshipFilter, LookupPermissionshipFilter, LookupPermissionshipFilter.All, "permissionshipFilter");
        const size = positiveLimit(request.pageSize ?? 1000, "pageSize");
        if (request.limit !== undefined)
            positiveLimit(request.limit, "limit");
        const wire = create(LookupResourcesRequestSchema, { resourceObjectType: request.resourceType, permission: request.permission, subject: protoSubject(request.subject), consistency: this.consistency(request.consistency), context: request.context === undefined ? undefined : jsonSnapshot(request.context) as JsonObject, optionalLimit: Math.min(size, request.limit ?? size), withDebug: mode(request.debug, LookupDebug, LookupDebug.Disabled, "debug") === LookupDebug.Enabled, optionalCursor: request.cursor === undefined ? undefined : create(CursorSchema, { token: request.cursor }) });
        const self = this;
        const limit = request.limit;
        return this.stream(options, async function* (op) {
            let total = 0;
            for (;;) {
                op.check();
                let count = 0;
                let cursor: string | undefined;
                for await (const resp of self.proto.permissions.lookupResources(clone(LookupResourcesRequestSchema, wire), op.callOptions())) {
                    op.check();
                    cursor = resp.afterResultCursor?.token;
                    count++;
                    const result = fromProtoLookupResource(resp);
                    if (filter === LookupPermissionshipFilter.HasPermissionOnly && result.permissionship !== "hasPermission")
                        continue;
                    total++;
                    yield result;
                    if (limit !== undefined && total >= limit)
                        return;
                }
                if (count < wire.optionalLimit || !cursor)
                    return;
                if (cursor === wire.optionalCursor?.token)
                    throw new SpiceDBError("Lookup cursor did not advance");
                wire.optionalCursor = create(CursorSchema, { token: cursor });
                wire.optionalLimit = Math.min(size, limit === undefined ? size : limit - total);
            }
        });
    }
    lookupSubjects(request: LookupSubjectsRequest, options?: CallOptions): AsyncIterableIterator<LookupSubject> {
        if (request.limit !== undefined)
            positiveLimit(request.limit, "limit");
        const wildcards = mode(request.wildcards, Wildcards, Wildcards.Default, "wildcards");
        const wire = create(LookupSubjectsRequestSchema, { resource: request.resource, permission: request.permission, subjectObjectType: request.subjectType, optionalSubjectRelation: request.subjectRelation ?? "", consistency: this.consistency(request.consistency), context: request.context === undefined ? undefined : jsonSnapshot(request.context) as JsonObject, optionalConcreteLimit: request.limit ?? 0, optionalCursor: request.cursor === undefined ? undefined : create(CursorSchema, { token: request.cursor }), wildcardOption: wildcards === Wildcards.Exclude ? LookupSubjectsRequest_WildcardOption.EXCLUDE_WILDCARDS : wildcards === Wildcards.Include ? LookupSubjectsRequest_WildcardOption.INCLUDE_WILDCARDS : LookupSubjectsRequest_WildcardOption.UNSPECIFIED });
        const self = this;
        return this.stream(options, async function* (op) {
            for await (const resp of self.proto.permissions.lookupSubjects(wire, op.callOptions())) {
                op.check();
                yield fromProtoLookupSubject(resp);
            }
        });
    }
    async expandPermissionTree(request: ExpandPermissionTreeRequest, options?: CallOptions): Promise<ExpandResult> {
        const wire = create(ExpandPermissionTreeRequestSchema, { resource: request.resource, permission: request.permission, consistency: this.consistency(request.consistency) });
        return this.unary(options, true, async (op) => { const resp = await this.proto.permissions.expandPermissionTree(wire, op.callOptions()); return immutableSnapshot({ treeRoot: fromProtoPermissionTree(resp.treeRoot), revision: serverToken(resp.expandedAt?.token) }); });
    }
    async importRelationships(relationships: Iterable<Relationship> | AsyncIterable<Relationship>, options?: CallOptions): Promise<bigint> {
        const op = this.operation(options, true);
        let input: Iterator<Relationship> | AsyncIterator<Relationship> | undefined;
        let producerClosed = false;
        const closeProducer = () => {
            if (producerClosed)
                return;
            producerClosed = true;
            // A producer may be parked on an uninterruptible await. Its return()
            // cannot delay releasing the transport or delivering caller cancellation.
            try {
                if (input?.return)
                    void Promise.resolve(input.return()).catch(() => { });
            }
            catch { /* Retain the original operation/producer failure. */ }
        };
        async function* batches() {
            let batch: ReturnType<typeof toProtoRelationship>[] = [];
            try {
                for (;;) {
                    op.check();
                    const item = await op.race(Promise.resolve(input!.next()));
                    if (item.done) {
                        producerClosed = true;
                        break;
                    }
                    batch.push(toProtoRelationship(item.value));
                    if (batch.length === 1000) {
                        yield create(ImportBulkRelationshipsRequestSchema, { relationships: batch });
                        batch = [];
                    }
                }
                if (batch.length)
                    yield create(ImportBulkRelationshipsRequestSchema, { relationships: batch });
            }
            catch (error) {
                op.fail(op.map(error));
                throw error;
            }
            finally {
                closeProducer();
            }
        }
        try {
            op.check();
            input = Symbol.asyncIterator in relationships ? relationships[Symbol.asyncIterator]() : relationships[Symbol.iterator]();
            return (await op.race(this.proto.permissions.importBulkRelationships(batches(), op.callOptions()))).numLoaded;
        }
        catch (error) {
            throw op.map(error);
        }
        finally {
            op.dispose();
            closeProducer();
        }
    }
    exportRelationships(request: ExportRelationshipsRequest, options?: CallOptions): AsyncIterableIterator<Relationship> {
        const wire = create(ExportBulkRelationshipsRequestSchema, { consistency: this.consistency(request.consistency), optionalLimit: positiveLimit(request.pageSize ?? 1000, "pageSize"), optionalRelationshipFilter: request.filter === undefined ? undefined : toProtoRelationshipFilter(request.filter), optionalCursor: request.cursor === undefined ? undefined : create(CursorSchema, { token: request.cursor }) });
        const self = this;
        return this.stream(options, async function* (op) {
            for await (const resp of self.proto.permissions.exportBulkRelationships(wire, op.callOptions()))
                for (const rel of resp.relationships) {
                    op.check();
                    yield fromProtoRelationship(rel);
                }
        });
    }
    async readSchema(options?: CallOptions): Promise<SchemaResult> { return this.unary(options, true, async (op) => { const resp = await this.proto.schema.readSchema(create(ReadSchemaRequestSchema), op.callOptions()); return immutableSnapshot({ schema: resp.schemaText, revision: serverToken(resp.readAt?.token) }); }); }
    async writeSchema(schema: string, options?: CallOptions): Promise<ZedToken> { return this.unary(options, false, async (op) => serverToken((await this.proto.schema.writeSchema(create(WriteSchemaRequestSchema, { schema }), op.callOptions())).writtenAt?.token)); }
    async reflectSchema(request: ReflectSchemaRequest, options?: CallOptions): Promise<ReflectSchemaResult> {
        const wire = create(ReflectSchemaRequestSchema, { consistency: this.consistency(request.consistency), optionalFilters: request.filters?.map(f => create(ReflectionSchemaFilterSchema, { optionalDefinitionNameFilter: f.definitionName ?? "", optionalCaveatNameFilter: f.caveatName ?? "", optionalRelationNameFilter: f.relationName ?? "", optionalPermissionNameFilter: f.permissionName ?? "" })) });
        return this.unary(options, true, async (op) => { const resp = await this.proto.schema.reflectSchema(wire, op.callOptions()); return immutableSnapshot({ definitions: resp.definitions.map(fromProtoSchemaDefinition), caveats: resp.caveats.map(fromProtoSchemaCaveat), revision: serverToken(resp.readAt?.token) }); });
    }
    async computablePermissions(request: ComputablePermissionsRequest, options?: CallOptions): Promise<RelationsResult> {
        const wire = create(ComputablePermissionsRequestSchema, { consistency: this.consistency(request.consistency), definitionName: request.definitionName, relationName: request.relationName, optionalDefinitionNameFilter: request.definitionNameFilter ?? "" });
        return this.unary(options, true, async (op) => { const resp = await this.proto.schema.computablePermissions(wire, op.callOptions()); return immutableSnapshot({ relations: resp.permissions.map(fromProtoRelationReference), revision: serverToken(resp.readAt?.token) }); });
    }
    async dependentRelations(request: DependentRelationsRequest, options?: CallOptions): Promise<RelationsResult> {
        const wire = create(DependentRelationsRequestSchema, { consistency: this.consistency(request.consistency), definitionName: request.definitionName, permissionName: request.permissionName });
        return this.unary(options, true, async (op) => { const resp = await this.proto.schema.dependentRelations(wire, op.callOptions()); return immutableSnapshot({ relations: resp.relations.map(fromProtoRelationReference), revision: serverToken(resp.readAt?.token) }); });
    }
    async diffSchema(request: DiffSchemaRequest, options?: CallOptions): Promise<SchemaDiffResult> {
        const wire = create(DiffSchemaRequestSchema, { consistency: this.consistency(request.consistency), comparisonSchema: request.comparisonSchema });
        return this.unary(options, true, async (op) => { const resp = await this.proto.schema.diffSchema(wire, op.callOptions()); return immutableSnapshot({ diffs: resp.diffs.map(fromProtoSchemaDiff), revision: serverToken(resp.readAt?.token) }); });
    }
    /** @experimental */
    async experimentalRegisterRelationshipCounter(name: string, filter: RelationshipFilter, options?: CallOptions): Promise<void> {
        const wire = create(ExperimentalRegisterRelationshipCounterRequestSchema, { name, relationshipFilter: toProtoRelationshipFilter(filter) });
        await this.unary(options, false, op => this.proto.experimental.experimentalRegisterRelationshipCounter(wire, op.callOptions()));
    }
    /** @experimental */
    async experimentalCountRelationships(name: string, options?: CallOptions): Promise<RelationshipCountResult> {
        return this.unary(options, true, async (op) => { const resp = await this.proto.experimental.experimentalCountRelationships(create(ExperimentalCountRelationshipsRequestSchema, { name }), op.callOptions()); return immutableSnapshot(resp.counterResult.case === "readCounterValue" ? { stillCalculating: false, count: resp.counterResult.value.relationshipCount, revision: serverToken(resp.counterResult.value.readAt?.token) } : { stillCalculating: true }); });
    }
    /** @experimental */
    async experimentalUnregisterRelationshipCounter(name: string, options?: CallOptions): Promise<void> { await this.unary(options, false, op => this.proto.experimental.experimentalUnregisterRelationshipCounter(create(ExperimentalUnregisterRelationshipCounterRequestSchema, { name }), op.callOptions())); }
    watch(request?: WatchRequest, options?: CallOptions): AsyncIterableIterator<WatchEvent> {
        if (request?.objectTypes?.length && request.relationshipFilters?.length)
            throw new InvalidArgumentError("watch objectTypes and relationshipFilters cannot be combined");
        const kinds = (request?.content ?? [WatchContent.Relationships]).map(value => {
            const content = mode(value, WatchContent, WatchContent.Relationships, "content");
            return { [WatchContent.Relationships]: WatchKind.INCLUDE_RELATIONSHIP_UPDATES,
                [WatchContent.Checkpoints]: WatchKind.INCLUDE_CHECKPOINTS,
                [WatchContent.Schema]: WatchKind.INCLUDE_SCHEMA_UPDATES }[content];
        });
        const wire = create(WatchRequestSchema, { optionalObjectTypes: [...(request?.objectTypes ?? [])], optionalStartCursor: request?.startRevision === undefined ? undefined : create(ZedTokenSchema, { token: tokenString(request.startRevision) }), optionalUpdateKinds: kinds, optionalRelationshipFilters: request?.relationshipFilters?.map(toProtoRelationshipFilter) });
        const self = this;
        return this.stream(options, async function* (op) {
            for await (const resp of self.proto.watch.watch(wire, op.callOptions())) {
                const updates: WatchChange[] = resp.updates.map(update => ({ operation: ({ [RelationshipUpdate_Operation.CREATE]: "create", [RelationshipUpdate_Operation.TOUCH]: "touch", [RelationshipUpdate_Operation.DELETE]: "delete" } as const)[update.operation as RelationshipUpdate_Operation.CREATE] ?? "unspecified", relationship: fromProtoRelationship(update.relationship ?? createRelationship()) }));
                op.check();
                yield { updates, changesThrough: serverToken(resp.changesThrough?.token), metadata: resp.optionalTransactionMetadata === undefined ? undefined : jsonSnapshot(resp.optionalTransactionMetadata, "metadata"), schemaUpdated: resp.schemaUpdated, isCheckpoint: resp.isCheckpoint };
            }
        }, false);
    }
    /** @experimental Counts matches without registering a persistent relationship counter. */
    async experimentalCountRelationshipsByFilter(filter: RelationshipFilter, options?: CallOptions): Promise<RelationshipCountResult> {
        const wire = create(ExperimentalCountRelationshipsByFilterRequestSchema, { relationshipFilter: toProtoRelationshipFilter(filter) });
        return this.unary(options, true, async (op) => { const response = await this.proto.materializeRelationships.experimentalCountRelationshipsByFilter(wire, op.callOptions()); return immutableSnapshot({ stillCalculating: false, count: response.relationshipCount, revision: serverToken(response.readAt?.token) }); });
    }
    /** @experimental Infinite permission-set events. Resume explicitly after interruption. */
    experimentalWatchPermissionSets(request?: WatchPermissionSetsRequest, options?: CallOptions): AsyncIterableIterator<PermissionSetEvent> {
        const wire = create(PermissionSets.WatchPermissionSetsRequestSchema, { optionalStartingAfter: request?.startRevision === undefined ? undefined : create(ZedTokenSchema, { token: tokenString(request.startRevision) }) });
        const self = this;
        return this.stream(options, async function* (op) { for await (const response of self.proto.permissionSets.watchPermissionSets(wire, op.callOptions())) {
            op.check();
            yield nativePermissionSetEvent(response);
        } }, false);
    }
    /** @experimental Automatically fetches cursor pages; limit bounds total yielded responses. */
    experimentalLookupPermissionSets(request: LookupPermissionSetsRequest, options?: CallOptions): AsyncIterableIterator<LookupPermissionSet> {
        const size = positiveLimit(request.pageSize ?? 1000, "pageSize");
        if (request.limit !== undefined)
            positiveLimit(request.limit, "limit");
        const wire = create(PermissionSets.LookupPermissionSetsRequestSchema, { limit: Math.min(size, request.limit ?? size), optionalAtRevision: request.revision === undefined ? undefined : create(ZedTokenSchema, { token: tokenString(request.revision) }), optionalStartingAfterCursor: request.cursor === undefined ? undefined : protoPermissionSetCursor(request.cursor) });
        const self = this;
        const limit = request.limit;
        return this.stream(options, async function* (op) {
            let count = 0;
            for (;;) {
                let cursor: PermissionSets.Cursor | undefined;
                op.check();
                for await (const response of self.proto.permissionSets.lookupPermissionSets(clone(PermissionSets.LookupPermissionSetsRequestSchema, wire), op.callOptions())) {
                    op.check();
                    cursor = response.cursor;
                    count++;
                    yield { change: response.change === undefined ? undefined : nativePermissionSetChange(response.change), cursor: response.cursor === undefined ? undefined : nativePermissionSetCursor(response.cursor) };
                    if (limit !== undefined && count >= limit)
                        return;
                }
                if (!cursor || cursor.completedMembers)
                    return;
                const previous = wire.optionalStartingAfterCursor;
                if (previous && permissionSetCursorKey(previous) === permissionSetCursorKey(cursor))
                    throw new SpiceDBError("Permission-set cursor did not advance");
                wire.optionalStartingAfterCursor = clone(PermissionSets.CursorSchema, cursor);
                wire.limit = Math.min(size, limit === undefined ? size : limit - count);
            }
        });
    }
    /** @experimental Returns snapshot file URLs, timestamp and revision without downloading files. */
    async experimentalDownloadPermissionSets(request?: DownloadPermissionSetsRequest, options?: CallOptions): Promise<DownloadPermissionSetsResult> {
        const wire = create(PermissionSets.DownloadPermissionSetsRequestSchema, { optionalAtRevision: request?.revision === undefined ? undefined : create(ZedTokenSchema, { token: tokenString(request.revision) }) });
        return this.unary(options, true, async (op) => { const response = await this.proto.permissionSets.downloadPermissionSets(wire, op.callOptions()); return immutableSnapshot({ files: response.files.map(file => ({ name: file.name, url: file.url })), ...(response.timestamp ? { timestamp: new Date(Number(response.timestamp.seconds) * 1000 + response.timestamp.nanos / 1e6) } : {}), revision: serverToken(response.atRevision?.token) }); });
    }
    /** @experimental Returns the raw roaring64 bitmap without decoding its IDs. */
    async experimentalRoaringLookupResources(request: RoaringLookupResourcesRequest, options?: CallOptions): Promise<RoaringLookupResourcesResult> {
        const wire = { consistency: this.consistency(request.consistency), resourceObjectType: request.resourceType, permission: request.permission, subject: protoSubject(request.subject) };
        return this.unary(options, true, async (op) => { const resp = await this.proto.materialize.experimentalRoaringLookupResources(wire, op.callOptions()); return immutableSnapshot({ bitmap: resp.bitmap, cardinality: resp.cardinality, revision: serverToken(resp.atRevision?.token) }); });
    }
}
function createRelationship() { return toProtoRelationship({ resource: { objectType: "", objectId: "" }, relation: "unspecified", subject: { object: { objectType: "", objectId: "" } } }); }
export function createSpiceDBClient(endpoint: string, token: string, options?: Omit<SpiceDBClientOptions, "endpoint" | "token">): SpiceDBClient { return new SpiceDBClient({ endpoint, token, ...options }); }
function protoPermissionSetCursor(cursor: PermissionSetCursor): PermissionSets.Cursor {
    if (!Number.isInteger(cursor.startingIndex) || cursor.startingIndex < 0 || cursor.startingIndex > 0xffffffff)
        throw new InvalidArgumentError("cursor.startingIndex must be a uint32 integer");
    if (!Number.isInteger(cursor.limit) || cursor.limit < 0 || cursor.limit > 0xffffffff)
        throw new InvalidArgumentError("cursor.limit must be a uint32 integer");
    return create(PermissionSets.CursorSchema, { ...cursor, token: cursor.token === undefined ? undefined : create(ZedTokenSchema, { token: tokenString(cursor.token) }) });
}
function nativePermissionSetCursor(cursor: PermissionSets.Cursor): PermissionSetCursor { return immutableSnapshot({ limit: cursor.limit, ...(cursor.token === undefined ? {} : { token: serverToken(cursor.token.token) }), startingIndex: cursor.startingIndex, completedMembers: cursor.completedMembers, startingKey: cursor.startingKey, cursor: cursor.cursor }); }
function permissionSetCursorKey(cursor: PermissionSets.Cursor): string { return `${cursor.token?.token}:${cursor.startingIndex}:${cursor.completedMembers}:${cursor.startingKey}:${cursor.cursor}`; }
function nativeSetReference(reference: PermissionSets.SetReference): PermissionSetReference { return { object: { objectType: reference.objectType, objectId: reference.objectId }, permissionOrRelation: reference.permissionOrRelation }; }
function nativePermissionSetChange(change: PermissionSets.PermissionSetChange): PermissionSetChange {
    return immutableSnapshot({ revision: serverToken(change.atRevision?.token), operation: change.operation === PermissionSets.PermissionSetChange_SetOperation.ADDED ? "added" : change.operation === PermissionSets.PermissionSetChange_SetOperation.REMOVED ? "removed" : "unspecified", ...(change.parentSet ? { parentSet: nativeSetReference(change.parentSet) } : {}), child: change.child.case === "childSet" ? { kind: "set", set: nativeSetReference(change.child.value) } : change.child.case === "childMember" ? { kind: "member", member: { object: { objectType: change.child.value.objectType, objectId: change.child.value.objectId }, ...(change.child.value.optionalPermissionOrRelation ? { permissionOrRelation: change.child.value.optionalPermissionOrRelation } : {}) } } : { kind: "unspecified" } });
}
function nativePermissionSetEvent(response: PermissionSets.WatchPermissionSetsResponse): PermissionSetEvent {
    switch (response.response.case) {
        case "change": return { kind: "change", change: nativePermissionSetChange(response.response.value) };
        case "completedRevision": return { kind: "completedRevision", revision: serverToken(response.response.value.token) };
        case "lookupPermissionSetsRequired": return { kind: "lookupRequired", revision: serverToken(response.response.value.requiredLookupAt?.token) };
        case "breakingSchemaChange": return { kind: "breakingSchemaChange", revision: serverToken(response.response.value.changeAt?.token), affectedPermissions: response.response.value.affectedPermissions.map(permission => ({ resourceType: permission.resourceType, permission: permission.permission, subjectType: permission.subjectType, ...(permission.optionalSubjectRelation ? { subjectRelation: permission.optionalSubjectRelation } : {}) })) };
        default: return { kind: "unspecified" };
    }
}
