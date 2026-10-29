"""Local validation errors, ref parsers and prebuilt transaction updates."""

import grpc
import pytest

from spicedb import (
    InvalidArgumentError,
    LocalValidationError,
    ObjectRef,
    Relationship,
    SpiceDBError,
    SubjectRef,
    Transaction,
    Update,
    UpdateOperation,
)
from spicedb.errors import code_of, to_spicedb_error
from spicedb.testing import InMemoryClient


# --- local validation is both InvalidArgumentError and ValueError -------


def test_malformed_triple_is_caught_by_except_value_error():
    with pytest.raises(ValueError):
        Relationship.from_triple("document", "viewer", "user:alice")


def test_malformed_triple_is_caught_by_except_invalid_argument_error():
    with pytest.raises(InvalidArgumentError) as caught:
        Relationship.from_triple("document", "viewer", "user:alice")
    err = caught.value
    assert isinstance(err, LocalValidationError)
    assert isinstance(err, grpc.RpcError)
    assert err.code() is grpc.StatusCode.INVALID_ARGUMENT
    assert code_of(err) is grpc.StatusCode.INVALID_ARGUMENT
    assert err.server_message is None
    assert err.trailing_metadata()  # rich status built from the code


def test_malformed_tuple_and_bad_option_raise_value_error():
    with pytest.raises(ValueError):
        Relationship.from_tuple("not a tuple")
    with pytest.raises(ValueError):
        Transaction().must_match("not a filter")  # type: ignore[arg-type]


def test_double_local_validation_is_a_value_error():
    fake = InMemoryClient()
    with pytest.raises(ValueError):
        fake.set_check_resolver("not callable")  # type: ignore[arg-type]


def test_server_invalid_argument_stays_plain_invalid_argument_error():
    err = SpiceDBError.from_code(grpc.StatusCode.INVALID_ARGUMENT, "bad request")
    assert type(err) is InvalidArgumentError
    assert not isinstance(err, ValueError)
    assert err.server_message == "bad request"

    class _ServerError(grpc.RpcError):
        def code(self):
            return grpc.StatusCode.INVALID_ARGUMENT

        def details(self):
            return "from the server"

        def trailing_metadata(self):
            return ()

    converted = to_spicedb_error(_ServerError())
    assert type(converted) is InvalidArgumentError
    assert not isinstance(converted, ValueError)


# --- ObjectRef / SubjectRef validation and parsers ----------------------


@pytest.mark.parametrize(
    "object_type,object_id",
    [
        ("", ""),
        ("document", ""),
        ("", "readme"),
        ("Document", "readme"),  # uppercase type
        ("d", "readme"),  # shorter than a definition name can be
        ("document", "has space"),
        ("document", "a:b"),
        ("document", "a#b"),
        ("document", "a@b"),
    ],
)
def test_object_ref_rejects_what_the_server_tuple_syntax_rejects(object_type, object_id):
    with pytest.raises(LocalValidationError):
        ObjectRef(object_type, object_id)


@pytest.mark.parametrize(
    "object_type,object_id",
    [
        ("document", "readme"),
        ("tenant/document", "a/b_c|d-e=f+g"),
        ("user", "*"),
        ("user", "A1"),
    ],
)
def test_object_ref_accepts_server_tuple_syntax(object_type, object_id):
    ref = ObjectRef(object_type, object_id)
    assert str(ref) == f"{object_type}:{object_id}"


def test_object_ref_parse():
    assert ObjectRef.parse("document:readme") == ObjectRef("document", "readme")
    for bad in ("", "document", "document:", ":readme", "document:a:b"):
        with pytest.raises(LocalValidationError):
            ObjectRef.parse(bad)
    with pytest.raises(LocalValidationError):
        ObjectRef.parse(None)  # type: ignore[arg-type]


def test_subject_ref_parse():
    assert SubjectRef.parse("user:alice") == SubjectRef(ObjectRef("user", "alice"))
    assert SubjectRef.parse("team:eng#member") == SubjectRef(ObjectRef("team", "eng"), "member")
    assert SubjectRef.parse("user:alice#...") == SubjectRef(ObjectRef("user", "alice"))
    assert SubjectRef.parse("user:*") == SubjectRef(ObjectRef("user", "*"))
    for bad in ("", "user", "user:alice#", "team:eng#member#x", "user:"):
        with pytest.raises(LocalValidationError):
            SubjectRef.parse(bad)


def test_parse_round_trips_str():
    for text in ("document:readme", "tenant/document:x"):
        assert str(ObjectRef.parse(text)) == text
    for text in ("user:alice", "team:eng#member"):
        assert str(SubjectRef.parse(text)) == text


def test_server_values_are_not_revalidated():
    from authzed.api.v1 import core_pb2
    from spicedb import protoconv

    # A server value outside today's local rules is surfaced as sent, never
    # turned into a local validation failure on a read path.
    ref = protoconv.object_from_proto(core_pb2.ObjectReference(object_type="X", object_id=""))
    assert (ref.object_type, ref.object_id) == ("X", "")


# --- prebuilt updates in a Transaction ----------------------------------


def _rel(text="document:readme#viewer@user:alice"):
    return Relationship.from_tuple(text)


def test_transaction_add_queues_a_prebuilt_update():
    update = Update(UpdateOperation.CREATE, _rel())
    txn = Transaction().add(update).touch(_rel("document:b#viewer@user:bob"))
    assert txn.updates == (update, Update(UpdateOperation.TOUCH, _rel("document:b#viewer@user:bob")))
    assert txn == Transaction().create(_rel()).touch(_rel("document:b#viewer@user:bob"))


def test_transaction_extend_queues_updates_in_order():
    updates = [
        Update(UpdateOperation.TOUCH, _rel()),
        Update(UpdateOperation.DELETE, _rel("document:b#viewer@user:bob")),
    ]
    txn = Transaction().extend(iter(updates))
    assert txn.updates == tuple(updates)
    assert not txn.is_idempotent()
    assert Transaction().extend([updates[0]]).is_idempotent()


def test_transaction_add_rejects_unspecified_and_non_updates():
    txn = Transaction()
    with pytest.raises(LocalValidationError):
        txn.add(Update(UpdateOperation.UNSPECIFIED, _rel()))
    with pytest.raises(LocalValidationError):
        txn.add(_rel())  # type: ignore[arg-type]
    assert len(txn) == 0


def test_transaction_extend_is_all_or_nothing():
    txn = Transaction().touch(_rel())
    with pytest.raises(LocalValidationError):
        txn.extend(
            [
                Update(UpdateOperation.TOUCH, _rel("document:b#viewer@user:bob")),
                Update(UpdateOperation.UNSPECIFIED, _rel()),
            ]
        )
    assert len(txn) == 1
    with pytest.raises(LocalValidationError):
        txn.extend(Update(UpdateOperation.TOUCH, _rel()))  # type: ignore[arg-type]


def test_double_writes_prebuilt_updates():
    fake = InMemoryClient()
    fake.write(Transaction().extend([Update(UpdateOperation.TOUCH, _rel())]))
    assert fake.relationships() == [_rel()]
