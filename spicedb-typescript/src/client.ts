import { create, type JsonObject } from "@bufbuild/protobuf";
import {
  createSpiceDBClient as createProtoClient,
  type ClientOptions as ProtoClientOptions,
  type SpiceDBProtoClient,
  type TlsOptions,
  CheckBulkPermissionsRequestItemSchema,
  CheckBulkPermissionsRequestSchema,
  CheckPermissionRequestSchema,
  ReadRelationshipsRequestSchema,
  WriteRelationshipsRequestSchema,
  LookupResourcesRequestSchema,
  LookupSubjectsRequestSchema,
  ExpandPermissionTreeRequestSchema,
  ExportBulkRelationshipsRequestSchema,
  ImportBulkRelationshipsRequestSchema,
  ReadSchemaRequestSchema,
  WriteSchemaRequestSchema,
  ReflectSchemaRequestSchema,
  ReflectionSchemaFilterSchema,
  ComputablePermissionsRequestSchema,
  DependentRelationsRequestSchema,
  DiffSchemaRequestSchema,
  ExperimentalRegisterRelationshipCounterRequestSchema,
  ExperimentalCountRelationshipsRequestSchema,
  ExperimentalUnregisterRelationshipCounterRequestSchema,
  WatchRequestSchema,
  WatchKind,
  ObjectReferenceSchema,
  SubjectReferenceSchema,
  RelationshipSchema,
  ZedTokenSchema,
  RelationshipUpdate_Operation,
} from "@spicedb/proto";

import { Consistency } from "./consistency.js";

import {
  type Relationship,
  type RelationshipFilterOptions,
  type LookupResourcesParams,
  type LookupSubjectsParams,
  type LookupResource,
  type LookupSubject,
  type CheckRequest,
  type CheckOptions,
  CheckResult,
  type WatchChange,
  type WatchEvent,
  type WatchOptions,
  type ExpandPermissionTreeParams,
  type ReflectSchemaOptions,
  type ComputablePermissionsParams,
  type DependentRelationsParams,
  type RelationReference,
  type RelationshipCountResult,
  type Transaction,
  type DeleteOptions,
  type PermissionTree,
  type SchemaDefinition,
  type SchemaCaveat,
  type SchemaDiff,
  toProtoRelationship,
  fromProtoRelationship,
  toProtoRelationshipFilter,
  toProtoDeleteRelationshipsRequest,
  fromProtoPermissionTree,
  fromProtoSchemaDefinition,
  fromProtoSchemaCaveat,
  fromProtoRelationReference,
  fromProtoSchemaDiff,
  fromProtoLookupResource,
  fromProtoLookupSubject,
  checkResultFromProto,
  checkResultFromBulkItem,
  mergeCheckContext,
} from "./types.js";

import {
  SpiceDBError,
  toSpiceDBError,
  toSpiceDBErrorFromStatus,
  isTransientError,
} from "./errors.js";

/**
 * The escape hatch's return type: the underlying proto client, carrying the
 * four generated Connect clients this library calls through. Re-exported from
 * the proto tier so a caller can name what {@link SpiceDBClient.raw} hands
 * back without depending on `@spicedb/proto` directly.
 */
export type { SpiceDBProtoClient };

/**
 * Caller-supplied TLS trust material for the secure path.
 *
 * Re-exported from the proto tier rather than restated here: this client hands
 * `options.tls` to `createProtoClient` untouched, so there is one contract and
 * it belongs where the transport is built. Two declarations of it would drift
 * silently -- a field added to one and not the other type-checks on both sides
 * and is simply dropped in between.
 */
export type { TlsOptions };

/**
 * Options for creating a SpiceDBClient.
 */
export interface SpiceDBClientOptions {
  endpoint: string;
  token: string;
  /**
   * Use plaintext (insecure) connection instead of TLS.
   *
   * By itself, this only permits a plaintext connection to a loopback
   * endpoint (localhost, 127.0.0.0/8, or ::1) -- see
   * root DESIGN.md, "RULE: Credentials over insecure transport require an
   * explicit opt-in". For a non-loopback endpoint, also pass
   * `allowInsecureRemoteCredentials: true`.
   */
  insecure?: boolean;
  /**
   * Explicit, separately named opt-in permitting `insecure: true` to target
   * a non-loopback endpoint. Named and separate from `insecure` on purpose:
   * a reader must not be able to mistake it for a default. Set this to
   * `true` only if you genuinely mean to send a bearer token in cleartext
   * to a remote host.
   */
  allowInsecureRemoteCredentials?: boolean;
  /**
   * Caller-supplied TLS trust material -- a private CA to verify SpiceDB
   * against, and optionally a client certificate for mutual TLS. See
   * `TlsOptions`.
   *
   * This never decides *whether* TLS is used: `insecure` alone does that.
   * Combining the two throws, rather than silently ignoring the material the
   * way `node:tls` would on a plaintext socket -- supplying a CA must not
   * become a quieter route to sending a bearer token in cleartext. See root
   * DESIGN.md, "RULE: Credentials over insecure transport require an explicit
   * opt-in".
   */
  tls?: TlsOptions;
  headers?: Record<string, string>;
  maxRetries?: number;
  /**
   * Milliseconds applied to every unary call that does not pass its own
   * `timeoutMs`. Defaults to `DEFAULT_TIMEOUT_MS` (30s), mirroring
   * `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites
   * `grpc/grpc-node#541`, a known gRPC failure mode where a channel that
   * accepts a connection but never answers produces no error at all).
   * Without a finite default, a wedged SpiceDB hangs every caller that
   * didn't opt in to a timeout -- in practice, most callers -- forever: the
   * connection looks fine at the transport level, so nothing ever times out
   * and nothing is ever produced to retry. See root DESIGN.md, "RULE: A
   * unary call must have a deadline".
   *
   * Deliberately NOT applied to streaming calls (`readRelationships`,
   * `lookupResources`, `lookupSubjects`, `watch`, `exportBulkRelationships`)
   * -- those are long-lived by design, and applying this default to them
   * would make the stream itself the outage (see DESIGN.md, "Streaming
   * calls MUST NOT inherit the unary default").
   */
  defaultTimeoutMs?: number;
}

const DEFAULT_MAX_RETRIES = 3;

const DEFAULT_TIMEOUT_MS = 30_000;

/**
 * Normalizes the two calling conventions shared by `checkPermissions`,
 * `checkAny`, and `checkAll` into a plain `{ checks, options }` pair:
 *
 * - Classic variadic form — `(consistency, check1, check2, ...)` — every
 *   positional argument after `consistency` is an individual
 *   {@link CheckRequest}. Distinguished from the array form purely by
 *   `Array.isArray`: a real `CheckRequest` is never an array, so this can
 *   never misfire on an existing call site.
 * - Explicit-array form — `(consistency, checks, options?)` — `checksOrFirst`
 *   is the full `CheckRequest[]` and `optionsOrSecond`, if present, is
 *   {@link CheckOptions}.
 *
 * This keeps the pre-existing rest-parameter overload's behavior byte-for-
 * byte unchanged (no `options` is ever produced for it), while giving the
 * array form a place to carry a call-level default context.
 * @internal
 */
function normalizeBulkCheckArgs(
  checksOrFirst: CheckRequest[] | CheckRequest | undefined,
  optionsOrSecond: CheckOptions | CheckRequest | undefined,
  rest: CheckRequest[],
): { checks: CheckRequest[]; options?: CheckOptions } {
  if (Array.isArray(checksOrFirst)) {
    return { checks: checksOrFirst, options: optionsOrSecond as CheckOptions | undefined };
  }
  const checks: CheckRequest[] = [];
  if (checksOrFirst !== undefined) {
    checks.push(checksOrFirst);
  }
  if (optionsOrSecond !== undefined) {
    checks.push(optionsOrSecond as CheckRequest);
  }
  checks.push(...rest);
  return { checks };
}

/**
 * How many relationships go into one `ImportBulkRelationships` request
 * message. Matches the batch size every other SpiceDB client uses.
 * @internal
 */
const IMPORT_BATCH_SIZE = 1000;

/**
 * How many items go into one `CheckBulkPermissions` request.
 *
 * SpiceDB rejects a request carrying more items than `maxBulkCheckCount` --
 * 10,000, a hard-coded const in `internal/services/v1/bulkcheck.go` with no
 * flag to raise or lower it -- with
 * `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto enforces
 * this: `CheckBulkPermissionsRequest.items` carries only a per-item
 * `required` rule, not a collection-size rule, so the limit lives solely in
 * server code and a client that forwards the caller's array unchanged fails
 * on large inputs. 1,000 leaves ten times' headroom and matches
 * `IMPORT_BATCH_SIZE` and the other clients' check batch size.
 * @internal
 */
const CHECK_BATCH_SIZE = 1000;

/**
 * Converts a caller's relationship sequence into the request stream
 * `importBulkRelationships` sends, batching as it goes.
 *
 * The batching is incremental on purpose. Only one batch is resident at a
 * time, so a caller can hand in a generator reading from a file or a database
 * cursor and import a dataset larger than memory. Materializing the sequence
 * here -- or accepting only an array -- would put the caller's whole dataset
 * in the heap twice, once as their relationships and once as protos, which is
 * the shape most likely to run out of memory on the one call whose entire
 * purpose is bulk volume.
 *
 * `for await` accepts both sync and async iterables, so the sync case costs
 * nothing extra here.
 * @internal
 */
async function* importBatches(
  relationships: Iterable<Relationship> | AsyncIterable<Relationship>,
) {
  let batch = [];
  for await (const rel of relationships) {
    batch.push(toProtoRelationship(rel));
    if (batch.length >= IMPORT_BATCH_SIZE) {
      yield { relationships: batch };
      batch = [];
    }
  }
  if (batch.length > 0) {
    yield { relationships: batch };
  }
}

/**
 * SpiceDBClient provides an idiomatic TypeScript interface to SpiceDB.
 *
 * All read methods require an explicit consistency parameter.
 * All write methods return an opaque revision string.
 */
export class SpiceDBClient {
  private readonly proto: SpiceDBProtoClient;
  private readonly maxRetries: number;
  private readonly defaultTimeoutMs: number;

  constructor(options: SpiceDBClientOptions) {
    this.proto = createProtoClient(options.endpoint, options.token, {
      insecure: options.insecure,
      allowInsecureRemoteCredentials: options.allowInsecureRemoteCredentials,
      tls: options.tls,
      headers: options.headers,
    });
    this.maxRetries = options.maxRetries ?? DEFAULT_MAX_RETRIES;
    this.defaultTimeoutMs = options.defaultTimeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  /**
   * Escape hatch: the underlying `SpiceDBProtoClient`, with the four generated
   * Connect clients (`permissions`, `schema`, `watch`, `experimental`) this
   * client makes its own calls through.
   *
   * Clearly-marked **secondary** API. Root DESIGN.md's "What NOT To Do" keeps
   * channels, stubs and metadata out of the primary surface and permits exactly
   * this -- "escape hatches for advanced use are acceptable as clearly marked
   * secondary API" -- so that a request the idiomatic methods cannot express
   * (an RPC or proto field not wrapped here, such as
   * `WriteRelationshipsRequest.optionalTransactionMetadata`) has a workaround
   * short of forking the client:
   *
   * ```ts
   * const { permissionship } = await client.raw().permissions.checkPermission({
   *   consistency: { requirement: { case: "fullyConsistent", value: true } },
   *   resource: { objectType: "document", objectId: "readme" },
   *   permission: "view",
   *   subject: { object: { objectType: "user", objectId: "jimmy" } },
   * });
   * ```
   *
   * Four things to know before reaching for it:
   *
   * - The `authorization` header comes free. It is set by a transport
   *   interceptor, so every raw call is authenticated exactly as an idiomatic
   *   one is -- unlike this repo's Python client, which authenticates per call.
   * - A raw call is a raw call: no `SpiceDBError` mapping (you catch
   *   Connect's `ConnectError`), no retry on a transient failure, and no
   *   `defaultTimeoutMs` -- pass `CallOptions.timeoutMs` yourself, or the call
   *   is unbounded.
   * - Do NOT call `close()` on the returned object. It is the same connection
   *   this client uses, and {@link SpiceDBClient.close} is what releases it;
   *   closing it here breaks every later call on this client.
   * - No stability promise beyond what `@connectrpc/connect` and the generated
   *   `@spicedb/proto` clients give. They are those packages' objects, and this
   *   client will not shim over a change in either.
   *
   * It is an accessor, never a constructor: it takes no endpoint, token, or
   * transport setting and hands back a client that already exists, so it cannot
   * become a second construction path around the guard in
   * `createSpiceDBClient` -- root DESIGN.md, "RULE: Credentials over insecure
   * transport require an explicit opt-in".
   */
  raw(): SpiceDBProtoClient {
    return this.proto;
  }

  /**
   * Resolves a per-call `timeoutMs` override against `this.defaultTimeoutMs`.
   * `undefined` means "use the client default" -- there is deliberately no
   * way to make an unbounded unary call. See root DESIGN.md, "RULE: A unary
   * call must have a deadline".
   */
  private effectiveTimeoutMs(timeoutMs?: number): number {
    return timeoutMs ?? this.defaultTimeoutMs;
  }

  /**
   * Creates an `AbortController` for one streaming attempt, linked to an
   * optional caller-supplied `AbortSignal` so external cancellation
   * propagates too. Returns `[controller, cleanup]` -- every streaming
   * method MUST call `cleanup()` from a `finally` block wrapping the
   * attempt, whether it succeeded, threw, or was abandoned via the
   * caller's `for await` `break` (which resumes the generator through
   * `.return()`, unwinding through the same `finally` as any other exit
   * path).
   *
   * `cleanup()` unconditionally aborts `controller`. This is what actually
   * releases the underlying HTTP/2 stream on abandonment: Connect-ES's own
   * server-streaming iterator deliberately omits `return()`/`throw()` (see
   * its `run-call.js`, "We deliberately omit throw/return"), so a bare
   * `break` in consuming code never reaches the transport by itself.
   * Passing `controller.signal` as `CallOptions.signal` on the underlying
   * call gives Connect-ES an explicit cancellation path instead -- root
   * DESIGN.md, "RULE: Abandoning a stream must release it", clause 3,
   * names this exact trap. See also `close()`, which releases the whole
   * connection rather than one stream.
   */
  private linkedAbortController(external?: AbortSignal): [AbortController, () => void] {
    const controller = new AbortController();
    if (!external) {
      return [controller, () => controller.abort()];
    }
    if (external.aborted) {
      controller.abort(external.reason);
      return [controller, () => controller.abort()];
    }
    const onAbort = () => controller.abort(external.reason);
    external.addEventListener("abort", onAbort, { once: true });
    return [
      controller,
      () => {
        external.removeEventListener("abort", onAbort);
        controller.abort();
      },
    ];
  }

  /**
   * Releases the underlying transport. Idempotent -- safe to call more than
   * once, including concurrently with itself (delegates to
   * `SpiceDBProtoClient.close`, which guards with a boolean flag).
   *
   * Every streaming call on this client (`readRelationships`,
   * `lookupResources`, `lookupSubjects`, `watch`, `exportBulkRelationships`)
   * shares this one transport, and there was previously no way to release
   * it deterministically. See root DESIGN.md, "RULE: Abandoning a stream
   * must release it".
   */
  close(): void {
    this.proto.close();
  }

  // ---------------------------------------------------------------------------
  // Permission Checks
  // ---------------------------------------------------------------------------

  /**
   * Checks whether the subject has the given permission on the resource.
   *
   * Uses the single-check `CheckPermission` RPC directly (not routed
   * through `CheckBulkPermissions`).
   *
   * `options.context`, when supplied, is a call-level default caveat
   * context. It is merged key-by-key with `check.context` — `check.context`
   * wins on conflict, and default keys `check.context` does not mention are
   * retained (see {@link CheckOptions.context} for the exact rule). For a
   * single check this mostly reads as "either supplies context," but it
   * keeps the same `CheckOptions` shape usable across all four check
   * surfaces.
   *
   * @returns A {@link CheckResult}. Use `result.hasPermission()` for the
   *          common case — it is `true` ONLY when the server's answer is an
   *          unconditional grant. A `"conditionalPermission"` result means
   *          the server needed caveat context that was not supplied in
   *          `check.context`/`options.context`; it is NOT a grant, and
   *          `result.hasPermission()` returns `false` for it.
   */
  async checkPermission(
    consistency: Consistency,
    check: CheckRequest,
    options?: CheckOptions,
  ): Promise<CheckResult> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.checkPermission(
        create(CheckPermissionRequestSchema, {
          consistency: consistency._toProto(),
          resource: create(ObjectReferenceSchema, {
            objectType: check.resourceType,
            objectId: check.resourceId,
          }),
          permission: check.permission,
          subject: create(SubjectReferenceSchema, {
            object: create(ObjectReferenceSchema, {
              objectType: check.subjectType,
              objectId: check.subjectId,
            }),
            optionalRelation: check.subjectRelation ?? "",
          }),
          context: mergeCheckContext(options?.context, check.context),
        }),
        { timeoutMs },
      );
      return checkResultFromProto(resp);
    });
  }

  /**
   * Checks multiple permissions in a single bulk request.
   *
   * Two calling conventions:
   * - The classic variadic form (`consistency, check1, check2, ...`) —
   *   unchanged, and it never sets a call-level default.
   * - An explicit-array form (`consistency, checks, options`) that
   *   additionally accepts {@link CheckOptions}. `options.context` is a
   *   call-level default caveat context fanned out onto every item (the
   *   wire has no request-level context field — see
   *   {@link CheckOptions.context} for the exact per-item merge rule with
   *   each check's own `context`).
   *
   * A per-item failure (e.g. an internal error evaluating one specific
   * check) is surfaced by throwing a typed {@link SpiceDBError} — it is
   * NEVER silently coerced into a result, since that would make a
   * permission-denied, an invalid-argument, and an internal server error
   * all indistinguishable from a real "no permission" answer.
   *
   * Large inputs are split automatically into requests of at most 1,000
   * items and the responses concatenated in input order — SpiceDB rejects a
   * single request carrying more than 10,000. An empty `checks` sends no
   * request at all and resolves to `[]`.
   *
   * @returns An array of {@link CheckResult}, one per check request, in the
   *          same order. Results within one request share a `checkedAt` —
   *          the response carries a single token for the whole batch, not
   *          one per item — so an input large enough to be split can carry
   *          more than one token across the returned array.
   */
  async checkPermissions(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<CheckResult[]>;
  async checkPermissions(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<CheckResult[]>;
  async checkPermissions(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<CheckResult[]> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    return this.runBulkCheck(consistency, checks, options);
  }

  /**
   * Shared implementation behind {@link checkPermissions}, {@link checkAny},
   * and {@link checkAll} — all three are aggregates/pass-throughs over the
   * same `CheckBulkPermissions` request, so the request-building and
   * response-mapping logic (including the call-level `options.context`
   * merge) lives here once.
   * @internal
   */
  private async runBulkCheck(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<CheckResult[]> {
    // Zero checks sends nothing at all. An empty request is not a cheaper
    // way to ask nothing -- it is a round trip whose only possible answer is
    // the empty array, and `checkAll` already treats an aggregate over zero
    // checks as `false` rather than a grant.
    if (checks.length === 0) {
      return [];
    }

    // One request per chunk of `CHECK_BATCH_SIZE`, results concatenated in
    // input order so `results[i]` still corresponds to `checks[i]` across
    // the chunk boundary. A caller passing fewer than `CHECK_BATCH_SIZE`
    // checks -- the overwhelmingly common case -- still makes exactly one
    // request. Retry is per chunk, so a transient failure on the third
    // chunk never re-sends the first two.
    const results: CheckResult[] = [];
    for (let start = 0; start < checks.length; start += CHECK_BATCH_SIZE) {
      results.push(
        ...(await this.runBulkCheckChunk(
          consistency,
          checks.slice(start, start + CHECK_BATCH_SIZE),
          start,
          options,
        )),
      );
    }
    return results;
  }

  /**
   * Issues one `CheckBulkPermissions` request for `checks` and maps the
   * response. `checks` is non-empty and no longer than `CHECK_BATCH_SIZE`;
   * `runBulkCheck` is what enforces both. Every response guard below --
   * the pair-count check and the malformed-oneof check -- therefore applies
   * per chunk, exactly as it applied to the whole request before chunking.
   *
   * `offset` is `checks`'s start index within the caller's full array. The
   * "check item N" message reports `offset + i`, not `i`: the index a caller
   * sees must be the one they can use to look up their own check. Reporting
   * the chunk-relative index would attribute the failing item to a different
   * resource entirely.
   * @internal
   */
  private async runBulkCheckChunk(
    consistency: Consistency,
    checks: CheckRequest[],
    offset: number,
    options?: CheckOptions,
  ): Promise<CheckResult[]> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.checkBulkPermissions(
        create(CheckBulkPermissionsRequestSchema, {
          consistency: consistency._toProto(),
          items: checks.map((check) =>
            create(CheckBulkPermissionsRequestItemSchema, {
              resource: create(ObjectReferenceSchema, {
                objectType: check.resourceType,
                objectId: check.resourceId,
              }),
              permission: check.permission,
              subject: create(SubjectReferenceSchema, {
                object: create(ObjectReferenceSchema, {
                  objectType: check.subjectType,
                  objectId: check.subjectId,
                }),
                optionalRelation: check.subjectRelation ?? "",
              }),
              context: mergeCheckContext(options?.context, check.context),
            }),
          ),
        }),
        { timeoutMs },
      );
      // The proto guarantees pairs are returned in request order but says
      // nothing about count. A short response would otherwise silently
      // desync results[i] from checks[i] for every item after the gap — one
      // resource's answer attributed to another — and `checkAll`'s
      // `.every()` would return `true` over an array missing the checks that
      // would have denied. Fail loudly instead of returning a
      // misaligned-but-"successful" array. The doc comment on
      // `checkPermissions` promises "one per check request, in the same
      // order"; this is what enforces it.
      if (resp.pairs.length !== checks.length) {
        throw new SpiceDBError(
          `checkBulkPermissions returned ${resp.pairs.length} pair(s) for ` +
            `${checks.length} request item(s)`,
        );
      }

      const checkedAt = resp.checkedAt?.token ?? "";
      return resp.pairs.map((pair, i) => {
        if (pair.response.case === "error") {
          // A per-item error MUST reach the caller as a typed error, not as
          // a falsy/failing CheckResult — see the doc comment above.
          throw toSpiceDBErrorFromStatus(pair.response.value);
        }
        if (pair.response.case === "item") {
          return checkResultFromBulkItem(pair.response.value, checkedAt);
        }
        // Malformed pair: the oneof has neither `item` nor `error` set. A
        // well-behaved server always sets one, so this should be unreachable
        // in practice. Throw, matching the other six clients' guard, rather
        // than degrading to an `unspecified` result: `unspecified` is
        // non-granting and `.map()` does preserve index alignment, so the
        // desync rationale above doesn't apply here — but it is
        // indistinguishable from a real server answer of "no permission",
        // which hides a broken server behind a plausible-looking denial.
        throw new SpiceDBError(
          `check item ${offset + i}: malformed CheckBulkPermissionsPair ` +
            `(neither item nor error set)`,
        );
      });
    });
  }

  /**
   * Returns `true` if the subject has ANY of the specified permissions
   * outright. A `"conditionalPermission"` result does NOT count as
   * granted — only results where {@link CheckResult.hasPermission} is
   * `true` are considered. This is deliberate and fail-closed.
   *
   * Accepts the same two calling conventions as `checkPermissions`,
   * including the explicit-array form with a call-level {@link CheckOptions}
   * default.
   */
  async checkAny(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean>;
  async checkAny(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<boolean>;
  async checkAny(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<boolean> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    const results = await this.runBulkCheck(consistency, checks, options);
    return results.some((r) => r.hasPermission());
  }

  /**
   * Returns `true` if the subject has ALL of the specified permissions
   * outright. A `"conditionalPermission"` result does NOT count as
   * granted — every result must satisfy {@link CheckResult.hasPermission}
   * for this to return `true`. This is deliberate and fail-closed.
   *
   * Accepts the same two calling conventions as `checkPermissions`,
   * including the explicit-array form with a call-level {@link CheckOptions}
   * default.
   *
   * Returns `false`, not the vacuous `true` that `Array.prototype.every`
   * yields on an empty array, when zero checks are given — "no checks to
   * run" is not "all checks passed".
   */
  async checkAll(
    consistency: Consistency,
    ...checks: CheckRequest[]
  ): Promise<boolean>;
  async checkAll(
    consistency: Consistency,
    checks: CheckRequest[],
    options?: CheckOptions,
  ): Promise<boolean>;
  async checkAll(
    consistency: Consistency,
    checksOrFirst?: CheckRequest[] | CheckRequest,
    optionsOrSecond?: CheckOptions | CheckRequest,
    ...rest: CheckRequest[]
  ): Promise<boolean> {
    const { checks, options } = normalizeBulkCheckArgs(
      checksOrFirst,
      optionsOrSecond,
      rest,
    );
    if (checks.length === 0) {
      return false;
    }
    const results = await this.runBulkCheck(consistency, checks, options);
    return results.every((r) => r.hasPermission());
  }

  // ---------------------------------------------------------------------------
  // Relationship Reads
  // ---------------------------------------------------------------------------

  /**
   * Reads relationships matching the given filter.
   *
   * Stopping early releases the underlying stream: a `for await` `break`
   * resumes this generator through `.return()`, whose `finally` aborts the
   * call's internal controller. Pass `options.signal` when the release has
   * to be driven from outside the loop instead -- a timeout, a request
   * cancellation, tearing down a component. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   *
   * @returns An async iterable of matching relationships.
   */
  async *readRelationships(
    filter: RelationshipFilterOptions,
    consistency: Consistency,
    options?: { signal?: AbortSignal },
  ): AsyncIterableIterator<Relationship> {
    const request = create(ReadRelationshipsRequestSchema, {
      consistency: consistency._toProto(),
      relationshipFilter: toProtoRelationshipFilter(filter),
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      const [controller, cleanup] = this.linkedAbortController(options?.signal);
      try {
        const stream = this.proto.permissions.readRelationships(request, {
          signal: controller.signal,
        });
        for await (const resp of stream) {
          if (resp.relationship) {
            yield fromProtoRelationship(resp.relationship);
            yielded++;
          }
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      } finally {
        cleanup();
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Writes
  // ---------------------------------------------------------------------------

  /**
   * Writes relationships as a single atomic transaction.
   *
   * @returns The revision at which the write was committed.
   */
  async write(txn: Transaction, options?: { timeoutMs?: number }): Promise<string> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.callOnce(async () => {
      const resp = await this.proto.permissions.writeRelationships(
        create(WriteRelationshipsRequestSchema, {
          updates: txn.updates,
          optionalPreconditions: txn.preconditions,
          optionalTransactionMetadata: txn.metadata as JsonObject | undefined,
        }),
        { timeoutMs },
      );
      return resp.writtenAt?.token ?? "";
    });
  }

  /**
   * Deletes all relationships matching the given filter.
   *
   * `options.mustMatch`/`options.mustNotMatch` add preconditions that guard
   * the delete: if a precondition fails, the server rejects the call and
   * deletes nothing. Mirrors spicedb-go's `WithDeleteMustMatch`/
   * `WithDeleteMustNotMatch` (client/relationships.go).
   *
   * `options.limit` bounds how many relationships this call deletes. If
   * more relationships match the filter than `limit`, only `limit` of them
   * are deleted by this call (the server requires
   * `optionalAllowPartialDeletions`, which this sets automatically whenever
   * `limit` is given, to permit that). Unlike spicedb-go's
   * `WithDeleteLimit`, this does not auto-page — it does not loop to delete
   * every match when the match count exceeds `limit`; call again with the
   * same filter to continue deleting what remains.
   *
   * @returns The revision at which the deletion was committed.
   */
  async deleteRelationships(
    filter: RelationshipFilterOptions,
    options?: DeleteOptions,
  ): Promise<string> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.callOnce(async () => {
      const resp = await this.proto.permissions.deleteRelationships(
        toProtoDeleteRelationshipsRequest(filter, options),
        { timeoutMs },
      );
      return resp.deletedAt?.token ?? "";
    });
  }

  // ---------------------------------------------------------------------------
  // Lookups
  // ---------------------------------------------------------------------------

  /**
   * Looks up all resources of the given type that the subject has the
   * specified permission on. Each result carries the permissionship (full
   * grant vs conditional on caveat context), and, for conditional results,
   * which caveat context was missing, and `lookedUpAt` — the revision the
   * result was computed at. Callers MUST check `permissionship` before
   * treating a result as a full grant.
   *
   * Stopping early releases the underlying stream: a `for await` `break`
   * resumes this generator through `.return()`, whose `finally` aborts the
   * call's internal controller. Pass `params.signal` when the release has
   * to be driven from outside the loop instead -- a timeout, a request
   * cancellation, tearing down a component. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   *
   * @returns An async iterable of {@link LookupResource}.
   */
  async *lookupResources(
    params: LookupResourcesParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupResource> {
    const request = create(LookupResourcesRequestSchema, {
      consistency: consistency._toProto(),
      resourceObjectType: params.resourceType,
      permission: params.permission,
      subject: create(SubjectReferenceSchema, {
        object: create(ObjectReferenceSchema, {
          objectType: params.subjectType,
          objectId: params.subjectId,
        }),
        optionalRelation: params.subjectRelation ?? "",
      }),
      context: params.context as JsonObject | undefined,
      optionalLimit: params.limit ?? 0,
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      const [controller, cleanup] = this.linkedAbortController(params.signal);
      try {
        const stream = this.proto.permissions.lookupResources(request, {
          signal: controller.signal,
        });
        for await (const resp of stream) {
          yield fromProtoLookupResource(resp);
          yielded++;
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      } finally {
        cleanup();
      }
    }
  }

  /**
   * Looks up all subjects of the given type that have the specified
   * permission on the resource.
   *
   * When a yielded `LookupSubject.subject` is the wildcard `"*"`, the server
   * has granted the permission to every subject of `subjectType` EXCEPT
   * those listed in `LookupSubject.excludedSubjects`. Callers MUST check
   * `excludedSubjects` before treating a wildcard match as a blanket grant,
   * or they risk granting access to subjects the server explicitly excluded.
   * `lookedUpAt` carries the revision the result was computed at.
   *
   * Stopping early releases the underlying stream: a `for await` `break`
   * resumes this generator through `.return()`, whose `finally` aborts the
   * call's internal controller. Pass `params.signal` when the release has
   * to be driven from outside the loop instead -- a timeout, a request
   * cancellation, tearing down a component. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   *
   * @returns An async iterable of {@link LookupSubject}.
   */
  async *lookupSubjects(
    params: LookupSubjectsParams,
    consistency: Consistency,
  ): AsyncIterableIterator<LookupSubject> {
    const request = create(LookupSubjectsRequestSchema, {
      consistency: consistency._toProto(),
      resource: create(ObjectReferenceSchema, {
        objectType: params.resourceType,
        objectId: params.resourceId,
      }),
      permission: params.permission,
      subjectObjectType: params.subjectType,
      optionalSubjectRelation: params.subjectRelation ?? "",
      context: params.context as JsonObject | undefined,
      optionalConcreteLimit: params.limit ?? 0,
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      const [controller, cleanup] = this.linkedAbortController(params.signal);
      try {
        const stream = this.proto.permissions.lookupSubjects(request, {
          signal: controller.signal,
        });
        for await (const resp of stream) {
          yield fromProtoLookupSubject(resp);
          yielded++;
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      } finally {
        cleanup();
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Expand
  // ---------------------------------------------------------------------------

  /**
   * Expands a permission tree for the given resource and permission.
   */
  async expandPermissionTree(
    consistency: Consistency,
    params: ExpandPermissionTreeParams,
  ): Promise<{ expandedAt: string; treeRoot: PermissionTree }> {
    const timeoutMs = this.effectiveTimeoutMs(params.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.permissions.expandPermissionTree(
        create(ExpandPermissionTreeRequestSchema, {
          consistency: consistency._toProto(),
          resource: create(ObjectReferenceSchema, {
            objectType: params.resourceType,
            objectId: params.resourceId,
          }),
          permission: params.permission,
        }),
        { timeoutMs },
      );
      return {
        expandedAt: resp.expandedAt?.token ?? "",
        treeRoot: fromProtoPermissionTree(resp.treeRoot),
      };
    });
  }

  // ---------------------------------------------------------------------------
  // Bulk Operations
  // ---------------------------------------------------------------------------

  /**
   * Imports relationships in bulk, in a single transaction.
   *
   * `relationships` is any iterable -- an array, a generator, an async
   * generator, anything with `Symbol.iterator` or `Symbol.asyncIterator`.
   * Relationships are converted and batched as they are pulled, so importing
   * a dataset larger than memory only requires that the caller produce it
   * lazily:
   *
   * ```ts
   * async function* fromCursor() {
   *   for await (const row of db.query("SELECT ...")) {
   *     yield { resourceType: "document", resourceId: row.id, ... };
   *   }
   * }
   * await client.importBulkRelationships(fromCursor());
   * ```
   *
   * An array still works exactly as before -- arrays are iterable -- and is
   * the right choice when the data is already in memory.
   *
   * The sequence is consumed exactly once. This call is never retried
   * automatically (a bulk import is a mutation; root DESIGN.md, "RULE:
   * Automatic retry is for idempotent operations only"), so a single pass is
   * all it needs. A caller who retries by hand must supply a fresh iterable,
   * since a spent generator yields nothing.
   *
   * `importBulkRelationships` is client-streaming, not unary: its duration
   * scales with the size of `relationships`, not with server latency, so
   * unlike every other method on this client it does NOT fall back to
   * `defaultTimeoutMs` (root DESIGN.md, "RULE: A unary call must have a
   * deadline", clause 3). Omitting `options.timeoutMs` here means this call
   * is unbounded; pass it explicitly to bound a bulk import.
   *
   * @returns The number of relationships loaded.
   */
  async importBulkRelationships(
    relationships: Iterable<Relationship> | AsyncIterable<Relationship>,
    options?: { timeoutMs?: number },
  ): Promise<bigint> {
    const timeoutMs = options?.timeoutMs;
    return this.callOnce(async () => {
      const resp = await this.proto.permissions.importBulkRelationships(
        importBatches(relationships),
        { timeoutMs },
      );
      return resp.numLoaded;
    });
  }

  /**
   * Exports all relationships, optionally filtered, as an async iterable.
   *
   * Stopping early releases the underlying stream: a `for await` `break`
   * resumes this generator through `.return()`, whose `finally` aborts the
   * call's internal controller. Pass `options.signal` when the release has
   * to be driven from outside the loop instead -- a timeout, a request
   * cancellation, tearing down a component. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   *
   * @returns An async iterable of relationships.
   */
  async *exportBulkRelationships(
    consistency: Consistency,
    filter?: RelationshipFilterOptions,
    options?: { signal?: AbortSignal },
  ): AsyncIterableIterator<Relationship> {
    const request = create(ExportBulkRelationshipsRequestSchema, {
      consistency: consistency._toProto(),
      optionalRelationshipFilter: filter
        ? toProtoRelationshipFilter(filter)
        : undefined,
    });
    let attempt = 0;
    for (;;) {
      let yielded = 0;
      const [controller, cleanup] = this.linkedAbortController(options?.signal);
      try {
        const stream = this.proto.permissions.exportBulkRelationships(request, {
          signal: controller.signal,
        });
        for await (const resp of stream) {
          for (const protoRel of resp.relationships) {
            yield fromProtoRelationship(protoRel);
            yielded++;
          }
        }
        return;
      } catch (err) {
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      } finally {
        cleanup();
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Schema
  // ---------------------------------------------------------------------------

  /**
   * Reads the current schema.
   *
   * @returns The schema text and revision.
   */
  async readSchema(options?: { timeoutMs?: number }): Promise<{ schema: string; revision: string }> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.schema.readSchema(
        create(ReadSchemaRequestSchema, {}),
        { timeoutMs },
      );
      return {
        schema: resp.schemaText,
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Writes (replaces) the current schema.
   *
   * @returns The revision at which the schema was written.
   */
  async writeSchema(schema: string, options?: { timeoutMs?: number }): Promise<string> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.callOnce(async () => {
      const resp = await this.proto.schema.writeSchema(
        create(WriteSchemaRequestSchema, { schema }),
        { timeoutMs },
      );
      return resp.writtenAt?.token ?? "";
    });
  }

  /**
   * Reflects the schema, returning definitions and caveats.
   */
  async reflectSchema(
    consistency: Consistency,
    options?: ReflectSchemaOptions,
  ): Promise<{
    definitions: SchemaDefinition[];
    caveats: SchemaCaveat[];
    revision: string;
  }> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const filters = options
        ? [
            create(ReflectionSchemaFilterSchema, {
              optionalDefinitionNameFilter:
                options.definitionNameFilter ?? "",
              optionalCaveatNameFilter: options.caveatNameFilter ?? "",
              optionalRelationNameFilter:
                options.relationNameFilter ?? "",
              optionalPermissionNameFilter:
                options.permissionNameFilter ?? "",
            }),
          ]
        : [];

      const resp = await this.proto.schema.reflectSchema(
        create(ReflectSchemaRequestSchema, {
          consistency: consistency._toProto(),
          optionalFilters: filters,
        }),
        { timeoutMs },
      );
      return {
        definitions: resp.definitions.map((def) => fromProtoSchemaDefinition(def)),
        caveats: resp.caveats.map((cav) => fromProtoSchemaCaveat(cav)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Computes the permissions that are computable for a given relation.
   */
  async computablePermissions(
    consistency: Consistency,
    params: ComputablePermissionsParams,
  ): Promise<{ permissions: RelationReference[]; revision: string }> {
    const timeoutMs = this.effectiveTimeoutMs(params.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.schema.computablePermissions(
        create(ComputablePermissionsRequestSchema, {
          consistency: consistency._toProto(),
          definitionName: params.definitionName,
          relationName: params.relationName,
          optionalDefinitionNameFilter: params.definitionNameFilter ?? "",
        }),
        { timeoutMs },
      );
      return {
        permissions: resp.permissions.map((p) => fromProtoRelationReference(p)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Finds the relations that a permission depends on.
   */
  async dependentRelations(
    consistency: Consistency,
    params: DependentRelationsParams,
  ): Promise<{ relations: RelationReference[]; revision: string }> {
    const timeoutMs = this.effectiveTimeoutMs(params.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.schema.dependentRelations(
        create(DependentRelationsRequestSchema, {
          consistency: consistency._toProto(),
          definitionName: params.definitionName,
          permissionName: params.permissionName,
        }),
        { timeoutMs },
      );
      return {
        relations: resp.relations.map((r) => fromProtoRelationReference(r)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  /**
   * Computes the diff between the current schema and a comparison schema.
   */
  async diffSchema(
    consistency: Consistency,
    comparisonSchema: string,
    options?: { timeoutMs?: number },
  ): Promise<{ diffs: SchemaDiff[]; revision: string }> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const resp = await this.proto.schema.diffSchema(
        create(DiffSchemaRequestSchema, {
          consistency: consistency._toProto(),
          comparisonSchema,
        }),
        { timeoutMs },
      );
      return {
        diffs: resp.diffs.map((d) => fromProtoSchemaDiff(d)),
        revision: resp.readAt?.token ?? "",
      };
    });
  }

  // ---------------------------------------------------------------------------
  // Experimental: Relationship Counters
  // ---------------------------------------------------------------------------

  /**
   * Registers a new relationship counter with the given filter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalRegisterRelationshipCounter(
    name: string,
    filter: RelationshipFilterOptions,
    options?: { timeoutMs?: number },
  ): Promise<void> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.callOnce(async () => {
      await this.proto.experimental.experimentalRegisterRelationshipCounter(
        create(ExperimentalRegisterRelationshipCounterRequestSchema, {
          name,
          relationshipFilter: toProtoRelationshipFilter(filter),
        }),
        { timeoutMs },
      );
    });
  }

  /**
   * Returns the count of relationships for a pre-registered counter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalCountRelationships(
    name: string,
    options?: { timeoutMs?: number },
  ): Promise<RelationshipCountResult> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.withRetry(async () => {
      const resp =
        await this.proto.experimental.experimentalCountRelationships(
          create(ExperimentalCountRelationshipsRequestSchema, { name }),
          { timeoutMs },
        );
      if (resp.counterResult.case === "counterStillCalculating") {
        return { stillCalculating: true };
      }
      if (resp.counterResult.case === "readCounterValue") {
        return {
          stillCalculating: false,
          count: Number(resp.counterResult.value.relationshipCount),
          revision: resp.counterResult.value.readAt?.token ?? "",
        };
      }
      return { stillCalculating: true };
    });
  }

  /**
   * Unregisters a previously registered relationship counter.
   * @experimental This API may change without following backwards compatibility rules.
   */
  async experimentalUnregisterRelationshipCounter(
    name: string,
    options?: { timeoutMs?: number },
  ): Promise<void> {
    const timeoutMs = this.effectiveTimeoutMs(options?.timeoutMs);
    return this.callOnce(async () => {
      await this.proto.experimental.experimentalUnregisterRelationshipCounter(
        create(ExperimentalUnregisterRelationshipCounterRequestSchema, {
          name,
        }),
        { timeoutMs },
      );
    });
  }

  // ---------------------------------------------------------------------------
  // Watch
  // ---------------------------------------------------------------------------

  /**
   * Watches for changes to relationships, returning an async iterable of events.
   *
   * Stopping early releases the underlying stream: a `for await` `break`
   * resumes this generator through `.return()`, whose `finally` aborts the
   * call's internal controller. Pass `options.signal` when the release has
   * to be driven from outside the loop instead -- a timeout, a request
   * cancellation, tearing down a component. See root DESIGN.md, "RULE:
   * Abandoning a stream must release it".
   */
  async *watch(
    options?: WatchOptions,
  ): AsyncIterableIterator<WatchEvent> {
    const req = create(WatchRequestSchema, {
      optionalObjectTypes: options?.objectTypes ?? [],
    });
    if (options?.startRevision) {
      req.optionalStartCursor = create(ZedTokenSchema, {
        token: options.startRevision,
      });
    }
    if (options?.includeCheckpoints) {
      // optionalUpdateKinds is empty-means-default (relationship updates
      // only, for backwards compatibility) -- a non-empty list is the exact
      // set requested, so asking for checkpoints must also spell out
      // relationship updates or the server would stop sending them.
      req.optionalUpdateKinds = [
        WatchKind.INCLUDE_RELATIONSHIP_UPDATES,
        WatchKind.INCLUDE_CHECKPOINTS,
      ];
    }

    let attempt = 0;
    for (;;) {
      let yielded = 0;
      const [controller, cleanup] = this.linkedAbortController(options?.signal);
      try {
        const stream = this.proto.watch.watch(req, { signal: controller.signal });
        for await (const resp of stream) {
          const changes: WatchChange[] = resp.updates.map((update) => {
            // Server-supplied data: an unrecognized operation
            // (OPERATION_UNSPECIFIED, or a future wire value added after this
            // client shipped) MUST NOT map to a write. Root DESIGN.md, "RULE:
            // A conversion that cannot preserve meaning must fail", clause 2:
            // server-supplied values the client does not recognise MUST NOT
            // raise, and MUST map to the safe, non-permissive default -- never
            // a grant, and never a write. TOUCH is a write, so it can only be
            // the mapping for an explicit OPERATION_TOUCH, never the default:
            // a cache or index mirror consuming this stream would otherwise
            // upsert a relationship that may in fact have been deleted.
            let operation: WatchChange["operation"];
            switch (update.operation) {
              case RelationshipUpdate_Operation.CREATE:
                operation = "create";
                break;
              case RelationshipUpdate_Operation.TOUCH:
                operation = "touch";
                break;
              case RelationshipUpdate_Operation.DELETE:
                operation = "delete";
                break;
              default:
                operation = "unspecified";
                break;
            }
            return {
              operation,
              relationship: update.relationship
                ? fromProtoRelationship(update.relationship)
                : {
                    resourceType: "",
                    resourceId: "",
                    resourceRelation: "",
                    subjectType: "",
                    subjectId: "",
                  },
            };
          });

          yield {
            changes,
            revision: resp.changesThrough?.token ?? "",
            metadata: resp.optionalTransactionMetadata,
            schemaUpdated: resp.schemaUpdated,
            isCheckpoint: resp.isCheckpoint,
          };
          yielded++;
        }
        return;
      } catch (err) {
        // Retrying is only safe before any update has been yielded (stream
        // ESTABLISHMENT) — never retry mid-watch, since that would
        // replay/duplicate already-delivered updates.
        if (
          yielded === 0 &&
          (await this.shouldRetryEstablishment(attempt, err))
        ) {
          attempt++;
          continue;
        }
        throw toSpiceDBError(err);
      } finally {
        cleanup();
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Retry Logic
  // ---------------------------------------------------------------------------

  /**
   * Decides whether to retry a streaming RPC's ESTABLISHMENT after a
   * transient error, sleeping with the same backoff as `withRetry`.
   *
   * Callers MUST only invoke this when zero items have been yielded from
   * the current stream — retrying after any item has been yielded would
   * replay/duplicate it for the caller. This method only makes the
   * transient/attempt-budget decision; the zero-yielded guard is the
   * caller's responsibility.
   */
  /**
   * Full-jitter backoff delay in milliseconds: `uniform(0, cap)` rather than
   * a fixed `cap`. Plain exponential backoff has every client in a fleet
   * retry on the same schedule after a server restart, turning the recovery
   * into a thundering herd; sampling uniformly under the cap spreads
   * retries out instead.
   */
  private backoffMs(attempt: number): number {
    const cap = Math.min(100 * 2 ** attempt, 5000);
    return Math.random() * cap;
  }

  private async shouldRetryEstablishment(
    attempt: number,
    err: unknown,
  ): Promise<boolean> {
    if (!isTransientError(err) || attempt === this.maxRetries) {
      return false;
    }
    await new Promise((resolve) => setTimeout(resolve, this.backoffMs(attempt)));
    return true;
  }

  /**
   * Calls `fn` once, converting a thrown error, but never retrying.
   *
   * For mutations. A `WriteRelationships` containing `OPERATION_CREATE`, or
   * any request carrying preconditions, is not idempotent: if it commits
   * and the response is lost (a rolling restart, a proxy dropping the
   * connection), a retry would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION`
   * for a write that in fact succeeded, and the caller would wrongly
   * conclude it had failed. See DESIGN.md, "Automatic retry is for
   * idempotent operations only".
   */
  private async callOnce<T>(fn: () => Promise<T>): Promise<T> {
    try {
      return await fn();
    } catch (err) {
      throw toSpiceDBError(err);
    }
  }

  private async withRetry<T>(fn: () => Promise<T>): Promise<T> {
    let lastErr: unknown;
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      try {
        return await fn();
      } catch (err) {
        lastErr = err;
        if (!isTransientError(err) || attempt === this.maxRetries) {
          throw toSpiceDBError(err);
        }
        await new Promise((resolve) => setTimeout(resolve, this.backoffMs(attempt)));
      }
    }
    throw toSpiceDBError(lastErr);
  }
}

/**
 * Creates a SpiceDBClient connected to the given endpoint.
 *
 * @param endpoint - The SpiceDB server address (host:port)
 * @param token - Bearer token for authentication
 * @param options - Optional configuration
 */
export function createSpiceDBClient(
  endpoint: string,
  token: string,
  options?: {
    insecure?: boolean;
    /**
     * Explicit, separately named opt-in permitting `insecure: true` to
     * target a non-loopback endpoint. See root DESIGN.md, "RULE:
     * Credentials over insecure transport require an explicit opt-in".
     */
    allowInsecureRemoteCredentials?: boolean;
    /**
     * Caller-supplied TLS trust material — a private CA to verify SpiceDB
     * against, and optionally a client certificate for mutual TLS. See
     * `TlsOptions`, and {@link SpiceDBClientOptions.tls} for why
     * combining it with `insecure` throws rather than being ignored.
     */
    tls?: TlsOptions;
    headers?: Record<string, string>;
    maxRetries?: number;
    defaultTimeoutMs?: number;
  },
): SpiceDBClient {
  return new SpiceDBClient({
    endpoint,
    token,
    ...options,
  });
}
