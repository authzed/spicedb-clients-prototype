"""Synchronous SpiceDB client over the proto-generated gRPC stubs.

Mirrors spicedb.aio.client method-for-method. The generated stub classes work
over either a sync `grpc.Channel` or an aio channel, so both flavors share every
request builder and response mapper; only the call and iteration mechanics
differ.

Keep this file in lockstep with spicedb/aio/client.py -- tests/test_parity.py
fails the build if the two surfaces diverge.
"""

from __future__ import annotations

import random
import time
from collections.abc import Iterable, Iterator
from typing import TYPE_CHECKING, Any

import grpc
from authzed.api.v1 import (
    experimental_service_pb2,
    permission_service_pb2,
    schema_service_pb2,
)

if TYPE_CHECKING:
    from authzed.api.v1 import (
        experimental_service_pb2_grpc,
        permission_service_pb2_grpc,
        schema_service_pb2_grpc,
        watch_service_pb2_grpc,
    )

from spicedb import _mapping, _requests
from spicedb._auth import bearer_metadata, require_insecure_transport_allowed
from spicedb._tls import channel_credentials, require_tls_material_usable
from spicedb._requests import DEFAULT_PAGE_SIZE as _DEFAULT_PAGE_SIZE
from spicedb._requests import CHECK_BATCH_SIZE as _CHECK_BATCH_SIZE
from spicedb._requests import IMPORT_BATCH_SIZE as _IMPORT_BATCH_SIZE
from spicedb.consistency import Consistency
from spicedb.errors import is_transient, to_spicedb_error
from spicedb.raw import RawGrpc
from spicedb.types import (
    CheckResult,
    Filter,
    LookupResource,
    LookupSubject,
    PermissionTree,
    ReflectSchemaResult,
    RelationReference,
    Relationship,
    SchemaDiff,
    Transaction,
    WatchEvent,
    _permission_tree_from_proto,
    _schema_diff_from_proto,
)

_DEFAULT_MAX_RETRIES = 3

_DEFAULT_TIMEOUT_SECONDS = 30.0
"""Applied to every unary call that does not pass its own `timeout`.

Mirrors `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites
`grpc/grpc-node#541`, a known gRPC failure mode where a channel that accepts
a connection but never answers produces no error at all). Without a finite
default, a wedged SpiceDB hangs every caller that didn't opt in to a timeout
-- in practice, most callers -- forever: the connection looks fine at the
transport level, so nothing ever times out and nothing is ever produced to
retry. See root DESIGN.md, "RULE: A unary call must have a deadline".

Deliberately NOT applied to server-streaming calls (`read_relationships`,
`lookup_resources`, `lookup_subjects`, `watch`, `export_relationships`) --
those are long-lived by design, and applying this default to them would make
the stream itself the outage -- NOR to the client-streaming
`import_relationships`, whose duration scales with the size of the caller's
dataset rather than server latency, so no fixed default is correct for it
either. See root DESIGN.md, "RULE: A unary call must have a deadline",
clause 3.

What those two shapes get instead differs, and only one of them takes a
`timeout=`:

- The server-streaming calls take NO `timeout=` at all. A deadline is the
  wrong bound for a stream whose correct duration is "as long as the caller
  keeps consuming" -- any value would eventually fire mid-stream and look
  like a server fault. They are bounded by cancellation instead: stop
  consuming, and the generator's `finally` cancels the call (see DESIGN.md,
  "Stream lifecycle").
- `import_relationships` DOES take `timeout=`, since it is the caller's own
  upload and the caller is the one who knows the volume. It is opt-in and
  unbounded by default.
"""


class SpiceDBClient:
    """Idiomatic synchronous Python client for SpiceDB.

    Usage::

        with SpiceDBClient("localhost:50051", token="test", insecure=True) as client:
            ...

    By itself, ``insecure=True`` only permits a plaintext connection to a
    loopback endpoint (localhost, 127.0.0.0/8, ::1, or a unix socket target)
    -- the local-development case that is the entire reason it exists. See
    root DESIGN.md, "RULE: Credentials over insecure transport require an
    explicit opt-in".

    To reach a SpiceDB fronted by a private or corporate CA, pass that CA's
    roots as ``ca_cert`` (PEM bytes), and add ``client_cert``/``client_key``
    where the server requires mutual TLS::

        with SpiceDBClient(
            "spicedb.internal:443",
            token="test",
            ca_cert=pathlib.Path("/etc/ssl/certs/internal-ca.pem").read_bytes(),
        ) as client:
            ...

    Trust material never changes whether TLS is used: ``insecure`` alone
    decides that, and passing both is refused rather than silently ignored.

    Safe to build once at process start and reuse for the process lifetime;
    there is no event loop to bind to.
    """

    def __init__(
        self,
        endpoint: str,
        token: str,
        *,
        insecure: bool = False,
        allow_insecure_remote_credentials: bool = False,
        ca_cert: bytes | None = None,
        client_cert: bytes | None = None,
        client_key: bytes | None = None,
        max_retries: int = _DEFAULT_MAX_RETRIES,
        default_timeout: float = _DEFAULT_TIMEOUT_SECONDS,
    ):
        """``default_timeout`` (seconds) bounds every unary call that does not
        pass its own ``timeout=``. It is NOT applied to streaming calls
        (``read_relationships``, ``lookup_resources``, ``lookup_subjects``,
        ``watch``, ``export_relationships``), which are long-lived by design.

        ``allow_insecure_remote_credentials`` is the explicit, separately
        named opt-in required by root DESIGN.md, "RULE: Credentials over
        insecure transport require an explicit opt-in" before ``insecure=True``
        may be combined with a non-loopback ``endpoint``. A loopback endpoint
        (localhost, 127.0.0.0/8, ::1, or a unix socket target) needs no such
        opt-in -- that is the ordinary local-development case.

        ``ca_cert``, ``client_cert`` and ``client_key`` are PEM bytes
        configuring TLS for the secure path. Leave all three unset for the
        default: grpc's own trust source, as root DESIGN.md, "RULE: A
        system-TLS constructor must reach a real server", clause 1 requires.

        - ``ca_cert`` -- the root(s) used to verify SpiceDB's certificate,
          one or more concatenated PEM certificates. Supply this to reach a
          SpiceDB fronted by a private or corporate CA. It REPLACES grpc's
          bundled roots for this client rather than adding to them. That
          bundled set is compiled into grpc's C-core, so a CA installed in
          the host's own trust store is not otherwise honoured -- the exact
          hazard the rule above names, and the reason this parameter exists.
        - ``client_cert`` / ``client_key`` -- the client's own certificate
          chain and private key, for a server that requires mutual TLS. Both
          must be supplied together.

        None of the three enables or disables TLS; ``insecure`` alone decides
        that, and combining it with any of them is refused (see below) rather
        than silently ignored.

        :raises spicedb.errors.InvalidArgumentError: if ``insecure`` is True,
            ``endpoint`` is not loopback, and
            ``allow_insecure_remote_credentials`` is False; if ``insecure`` is
            True and any of ``ca_cert``/``client_cert``/``client_key`` is
            supplied, since a plaintext connection performs no handshake to
            apply them to; or if exactly one of ``client_cert``/``client_key``
            is supplied. Raised before any channel or credential is created.
        """
        require_insecure_transport_allowed(
            endpoint,
            insecure=insecure,
            allow_insecure_remote_credentials=allow_insecure_remote_credentials,
        )
        require_tls_material_usable(
            insecure=insecure,
            ca_cert=ca_cert,
            client_cert=client_cert,
            client_key=client_key,
        )
        self._endpoint = endpoint
        self._insecure = insecure
        self._ca_cert = ca_cert
        self._client_cert = client_cert
        self._client_key = client_key
        self._max_retries = max_retries
        self._default_timeout = default_timeout
        self._metadata = bearer_metadata(token)
        self._channel: grpc.Channel | None = None
        self._permissions: permission_service_pb2_grpc.PermissionsServiceStub | None = None
        self._schema: schema_service_pb2_grpc.SchemaServiceStub | None = None
        self._watch: watch_service_pb2_grpc.WatchServiceStub | None = None
        self._experimental: experimental_service_pb2_grpc.ExperimentalServiceStub | None = None

    def _ensure_channel(self) -> None:
        """Open the channel on first use.

        Lazy purely to mirror the aio flavor's construction semantics, so the
        two clients behave identically for callers that build early and use
        late. Unlike aio there is no loop to bind to.
        """
        if self._channel is not None:
            return

        if self._insecure:
            self._channel = grpc.insecure_channel(self._endpoint)
        else:
            self._channel = grpc.secure_channel(
                self._endpoint,
                channel_credentials(
                    self._ca_cert, self._client_cert, self._client_key
                ),
            )

        from authzed.api.v1 import (
            experimental_service_pb2_grpc,
            permission_service_pb2_grpc,
            schema_service_pb2_grpc,
            watch_service_pb2_grpc,
        )

        self._permissions = permission_service_pb2_grpc.PermissionsServiceStub(
            self._channel
        )
        self._schema = schema_service_pb2_grpc.SchemaServiceStub(self._channel)
        self._watch = watch_service_pb2_grpc.WatchServiceStub(self._channel)
        self._experimental = experimental_service_pb2_grpc.ExperimentalServiceStub(
            self._channel
        )

    def raw_grpc(self) -> RawGrpc:
        """Escape hatch: the live gRPC channel and the bearer-token metadata.

        Clearly-marked secondary API, per root DESIGN.md's "What NOT To Do"
        ("escape hatches for advanced use are acceptable as clearly marked
        secondary API"). Use it to reach a SpiceDB RPC, request field, or
        call option this client does not wrap yet, instead of forking it.
        Nothing here maps errors to :mod:`spicedb.errors`, retries transient
        failures, or applies ``default_timeout`` -- a raw call gets grpc's
        behavior, not this client's. See :class:`spicedb.raw.RawGrpc` for the
        usage pattern and the (deliberately thin) stability promise.

        Opens the channel if it is not open yet, so the returned channel is
        always usable. It stays owned by this client: ``close()`` closes it,
        and closing it yourself breaks every later call on this client.

        This is NOT a second way to connect. It hands back the channel this
        client already built and cannot take an endpoint, token, or transport
        setting, so it cannot route around the guard in ``__init__`` -- root
        DESIGN.md, "RULE: Credentials over insecure transport require an
        explicit opt-in".
        """
        self._ensure_channel()
        assert self._channel is not None  # _ensure_channel guarantees it
        return RawGrpc(channel=self._channel, metadata=tuple(self._metadata))

    def close(self) -> None:
        """Close the underlying gRPC channel. A no-op if never used."""
        if self._channel is not None:
            self._channel.close()
            self._channel = None

    def __enter__(self) -> SpiceDBClient:
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> bool:
        self.close()
        return False

    # ── Deadlines ───────────────────────────────────────────────────

    def _effective_timeout(self, timeout: float | None) -> float:
        """Resolve a per-call ``timeout`` against the client default.

        ``None`` (the default on every unary method) means "use
        ``default_timeout``" -- there is deliberately no way to request an
        unbounded unary call. See root DESIGN.md, "RULE: A unary call must
        have a deadline".
        """
        return timeout if timeout is not None else self._default_timeout

    # ── Retry helper ────────────────────────────────────────────────

    @staticmethod
    def _backoff_seconds(attempt: int) -> float:
        """Full-jitter backoff: uniform(0, cap) rather than a fixed delay.

        Plain exponential backoff has every client in a fleet retry on the
        same schedule after a server restart, turning the recovery into a
        thundering herd. Sampling uniformly under the cap spreads retries
        out instead.
        """
        cap = min(0.1 * (2**attempt), 5.0)
        return random.uniform(0, cap)

    def _with_retry(self, fn: Any) -> Any:
        """Call a function with exponential backoff on transient errors.

        Only for idempotent (read) calls -- see `_call_once` for mutations.
        """
        last_err: Exception | None = None
        for attempt in range(self._max_retries + 1):
            try:
                return fn()
            except grpc.RpcError as e:
                if not is_transient(e) or attempt == self._max_retries:
                    raise to_spicedb_error(e) from e
                last_err = e
                time.sleep(self._backoff_seconds(attempt))
        raise to_spicedb_error(last_err) from last_err  # type: ignore[arg-type]

    def _call_once(self, fn: Any) -> Any:
        """Call a function once, converting a gRPC error, but never retrying.

        For mutations. A `WriteRelationships` containing `OPERATION_CREATE`,
        or any request carrying preconditions, is not idempotent: if it
        commits and the response is lost (a rolling restart, a proxy
        dropping the connection), a retry surfaces `ALREADY_EXISTS` or
        `FAILED_PRECONDITION` for a write that in fact succeeded, and the
        caller wrongly concludes it failed. See DESIGN.md, "Automatic retry
        is for idempotent operations only".
        """
        try:
            return fn()
        except grpc.RpcError as e:
            raise to_spicedb_error(e) from e

    def _should_retry_establishment(self, attempt: int, err: grpc.RpcError) -> bool:
        """Decide whether to retry a streaming RPC's ESTABLISHMENT after a
        transient error, sleeping with the same backoff as `_with_retry`.

        Callers MUST only retry when zero items have been yielded from the
        current stream/page -- retrying after any item has been yielded would
        replay/duplicate it for the caller. This helper only makes the
        transient/attempt-budget decision; the zero-yielded guard is the
        caller's responsibility.
        """
        if not is_transient(err) or attempt == self._max_retries:
            return False
        time.sleep(self._backoff_seconds(attempt))
        return True

    # ── Permission checks ──────────────────────────────────────────

    def check_permission(
        self,
        consistency: Consistency,
        rel: Relationship,
        *,
        context: dict[str, Any] | None = None,
        timeout: float | None = None,
    ) -> CheckResult:
        """Check a single permission. Returns a CheckResult -- use
        `.has_permission` for the common bool case, or inspect
        `.permissionship` directly to distinguish a CONDITIONAL_PERMISSION
        result (caveat context was needed but not supplied) from a real
        denial.

        `context` supplies caveat context for this check. `rel` can also
        carry its own `Relationship.check_context`, which overrides `context`
        key-by-key for this one check (see `check_permissions` below).

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        results = self.check_permissions(
            consistency, rel, context=context, timeout=timeout
        )
        return results[0]

    def check_permissions(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
        timeout: float | None = None,
    ) -> list[CheckResult]:
        """Check multiple permissions via BulkCheckPermissions. Returns a
        list of CheckResult, one per relationship, in the same order.

        Large inputs are split automatically into requests of at most
        `_requests.CHECK_BATCH_SIZE` (1,000) items and the responses
        concatenated in input order -- SpiceDB rejects a single request
        carrying more than 10,000 items. An empty `rels` sends no request
        at all and returns `[]`.

        Results from one request share a `checked_at` (the response carries
        a single token for the whole request, not one per item), so an input
        large enough to be split carries more than one token across the
        returned list.

        `context` is a call-level default applied to every relationship's
        check item. A relationship built with its own
        `check_context` (e.g. `Relationship.from_triple(..., check_context=
        {...})`) overrides `context` for that one item -- merged key-by-key
        (this item's keys win on conflict; call-level keys the item doesn't
        mention are retained, NOT replaced wholesale). An item with no
        `check_context` inherits `context` unchanged. `check_context` is
        check-time-only and distinct from `Relationship.caveat_context`,
        which is written into a relationship at write time -- see the
        `Relationship` docstring.

        `timeout` (seconds) bounds **each request** this call makes,
        overriding the client's `default_timeout`. A check over more than
        `CHECK_BATCH_SIZE` relationships is split into one request per
        chunk, and both this deadline and the retry budget apply to each
        chunk independently, not to the call as a whole -- worst-case wall
        time is `ceil(len(rels) / CHECK_BATCH_SIZE) * timeout`. That is
        deliberate: one deadline spanning every chunk would make a large
        check fail purely for being large, and a shared retry budget would
        let one flaky chunk exhaust the allowance for the rest."""
        self._ensure_channel()
        # Zero relationships sends nothing at all. An empty request is not a
        # cheaper way to ask nothing -- it is a round trip whose only
        # possible answer is the empty list, and `check_all` already treats
        # an aggregate over zero checks as False rather than a grant.
        if not rels:
            return []

        # One request per chunk of `CHECK_BATCH_SIZE`, results concatenated
        # in input order so results[i] still corresponds to rels[i] across
        # the chunk boundary. A caller passing fewer than `CHECK_BATCH_SIZE`
        # relationships -- the overwhelmingly common case -- still makes
        # exactly one request.
        results: list[CheckResult] = []
        for start in range(0, len(rels), _CHECK_BATCH_SIZE):
            results.extend(
                self._check_chunk(
                    consistency,
                    rels[start : start + _CHECK_BATCH_SIZE],
                    context,
                    timeout,
                    start,
                )
            )
        return results

    def _check_chunk(
        self,
        consistency: Consistency,
        rels: tuple[Relationship, ...],
        context: dict[str, Any] | None,
        timeout: float | None,
        offset: int,
    ) -> list[CheckResult]:
        """Issue one CheckBulkPermissions request for `rels` and map the
        response.

        `rels` is non-empty and no longer than `CHECK_BATCH_SIZE`;
        `check_permissions` is what enforces both. The pair-count guard in
        `_mapping.check_results` therefore applies per chunk, exactly as it
        applied to the whole request before chunking.

        `offset` is `rels`'s start index within the caller's full list, so
        the "check item N" message names the caller's own index rather than
        a chunk-relative one.
        """
        request = _requests.check_bulk_request(consistency, rels, context)
        t = self._effective_timeout(timeout)

        def _call() -> permission_service_pb2.CheckBulkPermissionsResponse:
            return self._permissions.CheckBulkPermissions(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return _mapping.check_results(resp, len(request.items), offset)

    def check_any(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
        timeout: float | None = None,
    ) -> bool:
        """Return True if any of the permission checks pass outright. Only
        `CheckResult.has_permission` results count -- a CONDITIONAL_PERMISSION
        result is not a grant, so it can never make this True."""
        self._ensure_channel()
        results = self.check_permissions(
            consistency, *rels, context=context, timeout=timeout
        )
        return any(r.has_permission for r in results)

    def check_all(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
        timeout: float | None = None,
    ) -> bool:
        """Return True if all of the permission checks pass outright. Only
        `CheckResult.has_permission` results count -- a CONDITIONAL_PERMISSION
        result is not a grant, so it makes this False.

        Returns False, not the vacuous True `all()` yields on an empty
        iterable, if `rels` is empty -- "no checks to run" is not "all
        checks passed"."""
        self._ensure_channel()
        if not rels:
            return False
        results = self.check_permissions(
            consistency, *rels, context=context, timeout=timeout
        )
        return all(r.has_permission for r in results)

    # ── Reads ───────────────────────────────────────────────────────

    def read_relationships(
        self,
        filter: Filter,
        consistency: Consistency,
    ) -> Iterator[Relationship]:
        """Read relationships matching the filter. Handles pagination automatically."""
        self._ensure_channel()
        cursor = None
        while True:
            request = _requests.read_relationships_request(
                filter, consistency, _DEFAULT_PAGE_SIZE, cursor
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                call = None
                try:
                    call = self._permissions.ReadRelationships(
                        request, metadata=self._metadata
                    )
                    for resp in call:
                        yielded += 1
                        count += 1
                        yield Relationship._from_proto(resp.relationship)
                        cursor = resp.after_result_cursor
                    break
                except grpc.RpcError as e:
                    if yielded == 0 and self._should_retry_establishment(attempt, e):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
                finally:
                    # Release the stream on every exit path -- abandonment
                    # included, where the generator is closed at the yield
                    # and this is the only thing that tells the server to
                    # stop. A no-op once the stream is already finished,
                    # and skipped entirely if opening it never returned.
                    if call is not None:
                        call.cancel()
            if count < _DEFAULT_PAGE_SIZE:
                return

    def lookup_resources(
        self,
        resource_type: str,
        permission: str,
        subject: Relationship | tuple[str, str],
        consistency: Consistency,
        *,
        context: dict[str, Any] | None = None,
    ) -> Iterator[LookupResource]:
        """Look up resources the subject has the given permission on.

        ``subject`` can be a Relationship (using its subject fields) or a
        tuple of ``("type:id", "optional_relation")``.

        Yields ``LookupResource`` — each result carries the permissionship
        (full grant vs conditional on caveat context) and, for conditional
        results, which caveat context was missing. Callers MUST check
        ``permissionship`` before treating a result as a full grant.
        Handles pagination automatically.
        """
        self._ensure_channel()
        subj_ref = _requests.subject_reference(subject)
        ctx_struct = _requests.context_struct(context)

        cursor = None
        while True:
            request = _requests.lookup_resources_request(
                resource_type,
                permission,
                subj_ref,
                ctx_struct,
                consistency,
                _DEFAULT_PAGE_SIZE,
                cursor,
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                call = None
                try:
                    call = self._permissions.LookupResources(
                        request, metadata=self._metadata
                    )
                    for resp in call:
                        yielded += 1
                        count += 1
                        yield _mapping.lookup_resource(resp)
                        cursor = resp.after_result_cursor
                    break
                except grpc.RpcError as e:
                    if yielded == 0 and self._should_retry_establishment(attempt, e):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
                finally:
                    # Release the stream on every exit path -- abandonment
                    # included, where the generator is closed at the yield
                    # and this is the only thing that tells the server to
                    # stop. A no-op once the stream is already finished,
                    # and skipped entirely if opening it never returned.
                    if call is not None:
                        call.cancel()
            if count < _DEFAULT_PAGE_SIZE:
                return

    def lookup_subjects(
        self,
        resource: tuple[str, str],
        permission: str,
        subject_type: str,
        consistency: Consistency,
        *,
        subject_relation: str = "",
        context: dict[str, Any] | None = None,
    ) -> Iterator[LookupSubject]:
        """Look up subjects that have the given permission on the resource.

        ``resource`` is a tuple of ``("type", "id")``.

        Yields ``LookupSubject``. When a yielded ``subject.subject_id`` is
        the wildcard ``"*"``, the server has granted the permission to every
        subject of ``subject_type`` EXCEPT those listed in
        ``excluded_subjects``. Callers MUST check ``excluded_subjects``
        before treating a wildcard match as a blanket grant, or they risk
        granting access to subjects the server explicitly excluded.
        """
        self._ensure_channel()
        ctx_struct = _requests.context_struct(context)
        request = _requests.lookup_subjects_request(
            resource,
            permission,
            subject_type,
            subject_relation,
            ctx_struct,
            consistency,
        )
        attempt = 0
        while True:
            yielded = 0
            call = None
            try:
                call = self._permissions.LookupSubjects(request, metadata=self._metadata)
                for resp in call:
                    yielded += 1
                    yield _mapping.lookup_subject(resp)
                return
            except grpc.RpcError as e:
                if yielded == 0 and self._should_retry_establishment(attempt, e):
                    attempt += 1
                    continue
                raise to_spicedb_error(e) from e
            finally:
                # Release the stream on every exit path -- abandonment
                # included, where the generator is closed at the yield
                # and this is the only thing that tells the server to
                # stop. A no-op once the stream is already finished,
                # and skipped entirely if opening it never returned.
                if call is not None:
                    call.cancel()

    # ── Writes ──────────────────────────────────────────────────────

    def write(self, txn: Transaction, *, timeout: float | None = None) -> str:
        """Execute a transaction and return the revision string.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.write_request(txn)
        t = self._effective_timeout(timeout)

        def _call() -> permission_service_pb2.WriteRelationshipsResponse:
            return self._permissions.WriteRelationships(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._call_once(_call)
        return resp.written_at.token

    def delete_relationships(
        self,
        filter: Filter,
        *,
        must_match: list[Filter] | None = None,
        must_not_match: list[Filter] | None = None,
        limit: int | None = None,
        timeout: float | None = None,
    ) -> str:
        """Delete relationships matching the filter. Returns the revision string.

        ``must_match``/``must_not_match`` add preconditions that guard the
        delete: if a precondition fails, the server rejects the call and
        deletes nothing. Mirrors spicedb-go's `WithDeleteMustMatch`/
        `WithDeleteMustNotMatch` (client/relationships.go).

        ``limit`` bounds how many relationships this single call deletes. If
        more relationships match the filter than ``limit``, only ``limit`` of
        them are deleted by this call (the server requires
        ``optional_allow_partial_deletions``, which this sets automatically
        whenever ``limit`` is given, to permit that). Unlike spicedb-go's
        `WithDeleteLimit`, this does not auto-page — it does not loop to
        delete every match when the match count exceeds ``limit``; call again
        with the same filter to continue deleting what remains.

        ``timeout`` (seconds) bounds this call, overriding the client's
        ``default_timeout``.
        """
        self._ensure_channel()
        request = _requests.delete_relationships_request(
            filter, must_match, must_not_match, limit
        )
        t = self._effective_timeout(timeout)

        def _call() -> permission_service_pb2.DeleteRelationshipsResponse:
            return self._permissions.DeleteRelationships(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._call_once(_call)
        return resp.deleted_at.token

    # ── Schema ──────────────────────────────────────────────────────

    def read_schema(self, *, timeout: float | None = None) -> str:
        """Read the current schema. Returns the schema text.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.ReadSchemaResponse:
            return self._schema.ReadSchema(
                schema_service_pb2.ReadSchemaRequest(),
                timeout=t,
                metadata=self._metadata,
            )

        resp = self._with_retry(_call)
        return resp.schema_text

    def write_schema(self, schema: str, *, timeout: float | None = None) -> str:
        """Write a schema. Returns the revision string.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.WriteSchemaResponse:
            return self._schema.WriteSchema(
                schema_service_pb2.WriteSchemaRequest(schema=schema),
                timeout=t,
                metadata=self._metadata,
            )

        resp = self._call_once(_call)
        return resp.written_at.token

    # ── Watch ───────────────────────────────────────────────────────

    def watch(
        self,
        *,
        object_types: list[str] | None = None,
        start_revision: str | None = None,
        include_checkpoints: bool = False,
    ) -> Iterator[WatchEvent]:
        """Watch for relationship changes. Yields `WatchEvent`s.

        `event.changes_through` is a resume point for a later `watch()`
        call's `start_revision` -- keep it if you need to survive a dropped
        stream. Pass `include_checkpoints=True` to also receive periodic
        checkpoint events (`event.is_checkpoint`, no updates); recommended
        behind a proxy that aborts idle connections, so the stream stays
        alive during quiet periods.
        """
        self._ensure_channel()
        request = _requests.watch_request(
            object_types, start_revision, include_checkpoints
        )
        attempt = 0
        while True:
            yielded = 0
            call = None
            try:
                call = self._watch.Watch(request, metadata=self._metadata)
                for resp in call:
                    yielded += 1
                    yield _mapping.watch_event(resp)
                return
            except grpc.RpcError as e:
                # Retrying is only safe before any update has been yielded
                # (stream ESTABLISHMENT) — never retry mid-watch, since
                # that would replay/duplicate already-delivered updates.
                if yielded == 0 and self._should_retry_establishment(attempt, e):
                    attempt += 1
                    continue
                raise to_spicedb_error(e) from e
            finally:
                # Release the stream on every exit path -- abandonment
                # included, where the generator is closed at the yield
                # and this is the only thing that tells the server to
                # stop. A no-op once the stream is already finished,
                # and skipped entirely if opening it never returned.
                if call is not None:
                    call.cancel()

    # ── Bulk operations ─────────────────────────────────────────────

    def import_relationships(
        self, relationships: Iterable[Relationship], *, timeout: float | None = None
    ) -> int:
        """Import relationships in bulk. Returns the number loaded.

        `relationships` may be a `list`, a generator, or any other iterable --
        it is consumed lazily, one batch (`_IMPORT_BATCH_SIZE` relationships)
        at a time, so a caller streaming in millions of relationships from a
        generator or a DB cursor is never forced to materialize the whole
        thing into a `list` first.

        `ImportBulkRelationships` is client-streaming: its duration scales with
        the size of `relationships`, not with server latency, so it does NOT
        inherit `default_timeout` (see root DESIGN.md, "RULE: A unary call
        must have a deadline", clause 3). Passing no `timeout` here means this
        call is unbounded; pass `timeout` (seconds) to bound it explicitly."""
        self._ensure_channel()
        try:
            resp = self._permissions.ImportBulkRelationships(
                _requests.import_batches(relationships, _IMPORT_BATCH_SIZE),
                timeout=timeout,
                metadata=self._metadata,
            )
            return resp.num_loaded
        except grpc.RpcError as e:
            raise to_spicedb_error(e) from e

    def export_relationships(
        self,
        consistency: Consistency,
        *,
        filter: Filter | None = None,
    ) -> Iterator[Relationship]:
        """Export relationships in bulk. Handles pagination automatically."""
        self._ensure_channel()
        cursor = None
        rel_filter = filter._to_proto() if filter else None
        while True:
            request = _requests.export_request(
                consistency, rel_filter, _DEFAULT_PAGE_SIZE, cursor
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                call = None
                try:
                    call = self._permissions.ExportBulkRelationships(
                        request, metadata=self._metadata
                    )
                    for resp in call:
                        for proto_rel in resp.relationships:
                            yield Relationship._from_proto(proto_rel)
                            yielded += 1
                            count += 1
                        cursor = resp.after_result_cursor
                    break
                except grpc.RpcError as e:
                    if yielded == 0 and self._should_retry_establishment(attempt, e):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
                finally:
                    # Release the stream on every exit path -- abandonment
                    # included, where the generator is closed at the yield
                    # and this is the only thing that tells the server to
                    # stop. A no-op once the stream is already finished,
                    # and skipped entirely if opening it never returned.
                    if call is not None:
                        call.cancel()
            if count < _DEFAULT_PAGE_SIZE:
                return

    # ── Expand ──────────────────────────────────────────────────────

    def expand_permission_tree(
        self,
        resource: tuple[str, str],
        permission: str,
        consistency: Consistency,
        *,
        timeout: float | None = None,
    ) -> tuple[PermissionTree, str]:
        """Expand a permission tree. Returns (tree, revision).

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.expand_request(resource, permission, consistency)
        t = self._effective_timeout(timeout)

        def _call() -> permission_service_pb2.ExpandPermissionTreeResponse:
            return self._permissions.ExpandPermissionTree(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return _permission_tree_from_proto(resp.tree_root), resp.expanded_at.token

    # ── Experimental: Relationship Counters ─────────────────────────

    def register_relationship_counter(
        self,
        name: str,
        filter: Filter,
        *,
        timeout: float | None = None,
    ) -> None:
        """Experimental: Register a relationship counter.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = (
            experimental_service_pb2.ExperimentalRegisterRelationshipCounterRequest(
                name=name,
                relationship_filter=filter._to_proto(),
            )
        )
        t = self._effective_timeout(timeout)

        def _call() -> Any:
            return self._experimental.ExperimentalRegisterRelationshipCounter(
                request, timeout=t, metadata=self._metadata
            )

        self._call_once(_call)

    def count_relationships(
        self,
        name: str,
        *,
        timeout: float | None = None,
    ) -> tuple[int, str]:
        """Experimental: Count relationships for a registered counter.

        Returns (count, revision).

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`.
        """
        self._ensure_channel()
        request = experimental_service_pb2.ExperimentalCountRelationshipsRequest(
            name=name,
        )
        t = self._effective_timeout(timeout)

        def _call() -> Any:
            return self._experimental.ExperimentalCountRelationships(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return resp.counter_result.relationship_count, resp.read_counter_at.token

    def unregister_relationship_counter(
        self,
        name: str,
        *,
        timeout: float | None = None,
    ) -> None:
        """Experimental: Unregister a relationship counter.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = (
            experimental_service_pb2.ExperimentalUnregisterRelationshipCounterRequest(
                name=name,
            )
        )
        t = self._effective_timeout(timeout)

        def _call() -> Any:
            return self._experimental.ExperimentalUnregisterRelationshipCounter(
                request, timeout=t, metadata=self._metadata
            )

        self._call_once(_call)

    # ── Experimental: Schema Reflection ─────────────────────────────

    def reflect_schema(
        self,
        consistency: Consistency,
        *,
        filters: list[str] | None = None,
        timeout: float | None = None,
    ) -> ReflectSchemaResult:
        """Experimental: Reflect the schema, returning its definitions and
        caveats.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.reflect_schema_request(consistency, filters)
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.ReflectSchemaResponse:
            return self._schema.ReflectSchema(request, timeout=t, metadata=self._metadata)

        resp = self._with_retry(_call)
        return ReflectSchemaResult._from_proto(resp)

    def diff_schema(
        self,
        consistency: Consistency,
        comparison_schema: str,
        *,
        timeout: float | None = None,
    ) -> list[SchemaDiff]:
        """Experimental: Diff two schemas, returning the list of differences.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.diff_schema_request(consistency, comparison_schema)
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.DiffSchemaResponse:
            return self._schema.DiffSchema(request, timeout=t, metadata=self._metadata)

        resp = self._with_retry(_call)
        return [_schema_diff_from_proto(d) for d in resp.diffs]

    # ── Schema Introspection: Computable Permissions / Dependent Relations ──

    def computable_permissions(
        self,
        consistency: Consistency,
        definition_name: str,
        relation_name: str,
        *,
        timeout: float | None = None,
    ) -> list[RelationReference]:
        """Return the permissions that are computable for the given relation
        on a definition.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.computable_permissions_request(
            consistency, definition_name, relation_name
        )
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.ComputablePermissionsResponse:
            return self._schema.ComputablePermissions(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return [RelationReference._from_proto(p) for p in resp.permissions]

    def dependent_relations(
        self,
        consistency: Consistency,
        definition_name: str,
        permission_name: str,
        *,
        timeout: float | None = None,
    ) -> list[RelationReference]:
        """Return the relations that the given permission depends on.

        `timeout` (seconds) bounds this call, overriding the client's
        `default_timeout`."""
        self._ensure_channel()
        request = _requests.dependent_relations_request(
            consistency, definition_name, permission_name
        )
        t = self._effective_timeout(timeout)

        def _call() -> schema_service_pb2.DependentRelationsResponse:
            return self._schema.DependentRelations(
                request, timeout=t, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return [RelationReference._from_proto(r) for r in resp.relations]
