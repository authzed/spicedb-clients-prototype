"""spicedb.testing: check resolution and lookups of the in-memory doubles.

A check resolves, in order, to a result registered for that exact question,
then to the caller's check resolver, then to the stored relationships (a
relationship whose relation is the permission's name and whose subject
matches exactly or by wildcard); anything else is no permission. Lookups
enumerate the objects of the requested type found in the stored
relationships and return those the check resolution grants.
"""

from __future__ import annotations

from datetime import datetime, timedelta, timezone

import grpc
import pytest

from spicedb import (
    Caveat,
    CheckResult,
    InvalidArgumentError,
    LookupPermissionshipFilter,
    ObjectRef,
    Operation,
    PermissionCheck,
    Permissionship,
    Relationship,
    SpiceDBError,
    SubjectRef,
    Transaction,
    UnavailableError,
    Wildcards,
    ZedToken,
    full,
)
from spicedb.testing import AsyncInMemoryClient, FaultStage, InMemoryClient, cursor_after, grant_all


def _unavailable(message: str) -> SpiceDBError:
    return SpiceDBError.from_code(grpc.StatusCode.UNAVAILABLE, message)


def _rel(text: str) -> Relationship:
    return Relationship.from_tuple(text)


def _user(name: str, relation: str | None = None) -> SubjectRef:
    return SubjectRef(ObjectRef("user", name), relation)


def _check(resource: str, permission: str, subject: SubjectRef, context=None) -> PermissionCheck:
    rt, ri = resource.split(":")
    return PermissionCheck(ObjectRef(rt, ri), permission, subject, context)


def _p(client: InMemoryClient, check: PermissionCheck, **kw) -> Permissionship:
    return client.check(check, consistency=full(), **kw).permissionship


# --- check resolution -------------------------------------------------------


def test_stored_direct_relationship_grants_its_relation_name():
    client = InMemoryClient([_rel("document:d#viewer@user:alice")])
    assert client.check(_check("document:d", "viewer", _user("alice")), consistency=full()).has_permission
    assert _p(client, _check("document:d", "viewer", _user("bob"))) is Permissionship.NO_PERMISSION
    # No schema is evaluated: "view" is not derived from "viewer".
    assert _p(client, _check("document:d", "view", _user("alice"))) is Permissionship.NO_PERMISSION
    assert _p(client, _check("document:other", "viewer", _user("alice"))) is Permissionship.NO_PERMISSION


def test_stored_subject_must_match_exactly_or_by_wildcard():
    client = InMemoryClient([
        _rel("document:d#viewer@team:eng#member"),
        _rel("document:pub#viewer@user:*"),
    ])
    assert _p(client, _check("document:d", "viewer", SubjectRef(ObjectRef("team", "eng"), "member"))) \
        is Permissionship.HAS_PERMISSION
    # A direct subject does not match a userset, nor a different relation.
    assert _p(client, _check("document:d", "viewer", SubjectRef(ObjectRef("team", "eng")))) \
        is Permissionship.NO_PERMISSION
    assert _p(client, _check("document:d", "viewer", SubjectRef(ObjectRef("team", "eng"), "admin"))) \
        is Permissionship.NO_PERMISSION
    # A wildcard grants every object of its type, and only that type.
    assert _p(client, _check("document:pub", "viewer", _user("anyone"))) is Permissionship.HAS_PERMISSION
    assert _p(client, _check("document:pub", "viewer", _user("*"))) is Permissionship.HAS_PERMISSION
    assert _p(client, _check("document:pub", "viewer", SubjectRef(ObjectRef("bot", "x")))) \
        is Permissionship.NO_PERMISSION
    # "..." is the direct subject relation.
    assert _p(client, _check("document:pub", "viewer", _user("anyone", "..."))) is Permissionship.HAS_PERMISSION


def test_stored_caveated_relationship_is_conditional_and_expired_one_is_absent():
    past = datetime.now(timezone.utc) - timedelta(hours=1)
    future = datetime.now(timezone.utc) + timedelta(hours=1)
    client = InMemoryClient([
        Relationship(ObjectRef("document", "d"), "viewer", _user("cav"), Caveat("ip_range")),
        Relationship(ObjectRef("document", "d"), "viewer", _user("old"), expiration=past),
        Relationship(ObjectRef("document", "d"), "viewer", _user("new"), expiration=future),
    ])
    result = client.check(_check("document:d", "viewer", _user("cav")), consistency=full())
    assert result.permissionship is Permissionship.CONDITIONAL_PERMISSION and not result
    assert _p(client, _check("document:d", "viewer", _user("old"))) is Permissionship.NO_PERMISSION
    assert _p(client, _check("document:d", "viewer", _user("new"))) is Permissionship.HAS_PERMISSION


def test_resolution_order_registered_then_resolver_then_stored():
    client = InMemoryClient([_rel("document:d#view@user:alice")])
    seen: list[PermissionCheck] = []

    def resolver(check: PermissionCheck):
        seen.append(check)
        if check.subject.object.object_id == "deny":
            return Permissionship.NO_PERMISSION
        if check.subject.object.object_id == "maybe":
            return CheckResult(Permissionship.CONDITIONAL_PERMISSION, ("ip",), ZedToken(""))
        if check.subject.object.object_id == "admin":
            return True
        return None  # defer to the stored relationships

    client.set_check_resolver(resolver)
    client.set_check_result(ObjectRef("document", "d"), "view", _user("deny"), Permissionship.HAS_PERMISSION)

    # 1. A registered result wins without consulting the resolver.
    assert _p(client, _check("document:d", "view", _user("deny"))) is Permissionship.HAS_PERMISSION
    assert seen == []
    # 2. The resolver answers next.
    assert _p(client, _check("document:d", "view", _user("admin"))) is Permissionship.HAS_PERMISSION
    maybe = client.check(_check("document:d", "view", _user("maybe")), consistency=full())
    assert maybe.permissionship is Permissionship.CONDITIONAL_PERMISSION
    assert list(maybe.missing_context) == ["ip"]
    assert maybe.checked_at == ZedToken("0")
    # 3. A resolver returning None defers to the stored relationships.
    assert _p(client, _check("document:d", "view", _user("alice"))) is Permissionship.HAS_PERMISSION
    assert _p(client, _check("document:d", "view", _user("bob"))) is Permissionship.NO_PERMISSION

    client.set_check_resolver(None)
    assert _p(client, _check("document:d", "view", _user("admin"))) is Permissionship.NO_PERMISSION


def test_resolver_sees_merged_context_and_bulk_items():
    client = InMemoryClient()
    seen: list[PermissionCheck] = []
    client.set_check_resolver(lambda c: seen.append(c) or None)
    client.check(_check("document:d", "view", _user("a"), {"k": 2}), consistency=full(), context={"k": 1, "j": 1})
    assert seen[-1].context == {"k": 2, "j": 1}
    results = client.check_bulk(
        [_check("document:d", "view", _user("a")), _check("document:e", "view", _user("b"))],
        consistency=full(),
    )
    assert [r.permissionship for r in results] == [Permissionship.NO_PERMISSION] * 2
    assert [str(c.resource) for c in seen[1:]] == ["document:d", "document:e"]


def test_grant_all_and_resolver_validation():
    client = InMemoryClient()
    client.set_check_resolver(grant_all)
    assert client.check(_check("document:any", "edit", _user("anyone")), consistency=full()).has_permission
    with pytest.raises(InvalidArgumentError):
        client.set_check_resolver("yes")  # type: ignore[arg-type]
    client.set_check_resolver(lambda _c: "granted")  # type: ignore[arg-type,return-value]
    with pytest.raises(InvalidArgumentError, match="check resolver"):
        client.check(_check("document:d", "view", _user("a")), consistency=full())


async def test_async_double_resolves_checks_the_same_way():
    client = AsyncInMemoryClient([_rel("document:d#viewer@user:alice")])
    assert (await client.check(_check("document:d", "viewer", _user("alice")), consistency=full())).has_permission
    client.set_check_resolver(lambda c: True if c.permission == "admin" else None)
    assert (await client.check(_check("document:d", "admin", _user("x")), consistency=full())).has_permission
    results = await client.check_bulk([_check("document:d", "viewer", _user("alice"))], consistency=full())
    assert results[0].has_permission
    client.set_check_resolver(grant_all)
    assert (await client.check(_check("document:z", "other", _user("y")), consistency=full())).has_permission


# --- lookup_resources -------------------------------------------------------


def _docs_client() -> InMemoryClient:
    return InMemoryClient([
        _rel("document:a#viewer@user:alice"),
        _rel("document:b#viewer@user:bob"),
        _rel("document:c#viewer@user:*"),
        _rel("folder:f#parent@document:d"),  # document:d is known only as a subject
        _rel("document:e#viewer@user:alice[ip_range]"),
    ])


def _lookup(client: InMemoryClient, **kw):
    args = dict(resource_type="document", permission="viewer", subject=_user("alice"), consistency=full())
    args.update(kw)
    return list(client.lookup_resources(**args))


def test_lookup_resources_filters_known_objects_by_check_resolution():
    client = _docs_client()
    items = _lookup(client)
    assert [(i.resource_id, i.permissionship) for i in items] == [
        ("a", Permissionship.HAS_PERMISSION),
        ("c", Permissionship.HAS_PERMISSION),
        ("e", Permissionship.CONDITIONAL_PERMISSION),
    ]
    assert items[2].partial_caveat is not None
    assert [i.after_result_cursor for i in items] == ["1", "2", "3"]
    assert {i.looked_up_at for i in items} == {ZedToken("0")}

    # Objects that appear only as subjects are enumerated too; a resolver can grant them.
    client.set_check_resolver(lambda c: True if c.resource.object_id == "d" else None)
    assert [i.resource_id for i in _lookup(client)] == ["a", "c", "d", "e"]
    client.set_check_resolver(grant_all)
    assert [i.resource_id for i in _lookup(client, subject=_user("zed"))] == ["a", "b", "c", "d", "e"]

    # Registered results apply to lookups as well.
    client.set_check_resolver(None)
    client.set_check_result(ObjectRef("document", "b"), "viewer", _user("alice"), Permissionship.HAS_PERMISSION)
    assert [i.resource_id for i in _lookup(client)] == ["a", "b", "c", "e"]


def test_lookup_resources_honours_filter_limit_and_cursor():
    client = _docs_client()
    only = _lookup(client, permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
    assert [i.resource_id for i in only] == ["a", "c"]
    assert [i.resource_id for i in _lookup(client, limit=2)] == ["a", "c"]
    resumed = _lookup(client, cursor=cursor_after(1))
    assert [(i.resource_id, i.after_result_cursor) for i in resumed] == [("c", "2"), ("e", "3")]
    # Results skipped by the permissionship filter still advance the cursor.
    client2 = InMemoryClient([
        _rel("document:a#viewer@user:alice[ip_range]"),
        _rel("document:b#viewer@user:alice"),
    ])
    items = _lookup(client2, permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("b", "2")]

    with pytest.raises(SpiceDBError) as raised:
        _lookup(client, cursor="nope")
    assert raised.value.code() is grpc.StatusCode.INVALID_ARGUMENT
    with pytest.raises(InvalidArgumentError):
        _lookup(client, limit=0)
    with pytest.raises(InvalidArgumentError):
        _lookup(client, cursor="")


def test_lookup_resources_records_calls_and_consults_fault_hooks():
    client = _docs_client()
    client.fail_next(Operation.LOOKUP_RESOURCES, _unavailable("down"))
    stream = client.lookup_resources(
        resource_type="document", permission="viewer", subject=_user("alice"), consistency=full(), limit=5,
    )
    with pytest.raises(UnavailableError):
        list(stream)
    call = client.calls_of(Operation.LOOKUP_RESOURCES)[0]
    assert call.request is None and call.options["limit"] == 5 and call.options["resource_type"] == "document"

    client.set_fault_hook(
        lambda f: _unavailable("item") if f.stage is FaultStage.ITEM and f.item == 1 else None
    )
    got = []
    with pytest.raises(UnavailableError):
        for item in client.lookup_resources(
            resource_type="document", permission="viewer", subject=_user("alice"), consistency=full(),
        ):
            got.append(item.resource_id)
    assert got == ["a"]


# --- lookup_subjects --------------------------------------------------------


def test_lookup_subjects_filters_known_subjects_by_check_resolution():
    client = InMemoryClient([
        _rel("document:d#viewer@user:alice"),
        _rel("document:d#viewer@user:carol[ip_range]"),
        _rel("document:other#viewer@user:bob"),
        _rel("document:d#viewer@team:eng#member"),
    ])

    def lookup(**kw):
        args = dict(resource=ObjectRef("document", "d"), permission="viewer", subject_type="user",
                    consistency=full())
        args.update(kw)
        return [(s.subject.subject_id, s.subject.permissionship) for s in client.lookup_subjects(**args)]

    assert lookup() == [("alice", Permissionship.HAS_PERMISSION), ("carol", Permissionship.CONDITIONAL_PERMISSION)]
    assert lookup(limit=1) == [("alice", Permissionship.HAS_PERMISSION)]
    assert lookup(subject_type="team", subject_relation="member") == [("eng", Permissionship.HAS_PERMISSION)]
    assert lookup(subject_type="team") == []

    # A wildcard relationship yields the wildcard and grants every known user.
    client.write(Transaction().touch(_rel("document:d#viewer@user:*")))
    assert [s for s, _ in lookup()] == ["alice", "carol", "bob", "*"]
    assert [s for s, _ in lookup(wildcards=Wildcards.EXCLUDE)] == ["alice", "carol", "bob"]

    client.set_check_resolver(lambda c: c.subject.object.object_id == "bob")
    assert [s for s, _ in lookup()] == ["bob"]
    assert client.calls_of(Operation.LOOKUP_SUBJECTS)[-1].options["subject_type"] == "user"


async def test_async_lookups():
    client = AsyncInMemoryClient([_rel("document:a#viewer@user:alice"), _rel("document:b#viewer@user:bob")])
    items = [i.resource_id async for i in client.lookup_resources(
        resource_type="document", permission="viewer", subject=_user("alice"), consistency=full(),
    )]
    assert items == ["a"]
    subjects = [s.subject.subject_id async for s in client.lookup_subjects(
        resource=ObjectRef("document", "b"), permission="viewer", subject_type="user", consistency=full(),
    )]
    assert subjects == ["bob"]
    client.fail_next(Operation.LOOKUP_SUBJECTS, _unavailable("down"))
    with pytest.raises(UnavailableError):
        [s async for s in client.lookup_subjects(
            resource=ObjectRef("document", "b"), permission="viewer", subject_type="user", consistency=full(),
        )]
    assert [c.operation for c in client.calls()] == [
        Operation.LOOKUP_RESOURCES, Operation.LOOKUP_SUBJECTS, Operation.LOOKUP_SUBJECTS,
    ]
