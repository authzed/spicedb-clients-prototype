"""Run inside a venv holding spicedb-python-proto, a bridged authzed-py and
spicedb-python (see test_bridge.py). Exits non-zero on the first failed check.

Proves, in one interpreter:
1. every shared generated module is the file spicedb-python-proto installed,
   never a copy from authzed-py;
2. the default descriptor pool holds exactly one authzed/api/v1/*.proto, the
   one those modules registered, and authzed-py's re-exported message classes
   are the canonical classes;
3. the two distributions own disjoint sets of files, so uninstalling one cannot
   delete the other's modules;
4. authzed-py's client and spicedb-python's client both work, against an
   in-process fake server and (when SPICEDB_ENDPOINT is set) a live SpiceDB.
"""

from __future__ import annotations

import os
import sys
from concurrent import futures
from importlib.metadata import distribution
from pathlib import Path

import grpc
from google.protobuf import descriptor_pool

import authzed.api.materialize.v0 as legacy_materialize
import authzed.api.v1 as legacy_v1
from authzed.api.v0 import core_pb2 as v0_core_pb2  # kept by the bridge
from authzed.api.v1 import core_pb2, permission_service_pb2, permission_service_pb2_grpc
from authzed.api.v1alpha1 import schema_pb2 as v1alpha1_schema_pb2  # kept by the bridge
from grpcutil import insecure_bearer_token_credentials

from spicedb.token import ZedToken
from spicedb import ObjectRef, PermissionCheck, SubjectRef, TransportSecurity, consistency
from spicedb.sync import SpiceDBClient

TOKEN = "somerandomkeyhere"


def check(cond: bool, msg: str) -> None:
    if not cond:
        print(f"FAIL: {msg}", file=sys.stderr)
        sys.exit(1)
    print(f"ok: {msg}")


def owned_files(dist_name: str) -> set[Path]:
    dist = distribution(dist_name)
    out = set()
    for f in dist.files or ():
        if ".dist-info" in str(f) or "__pycache__" in str(f) or str(f).startswith(".."):
            continue
        out.add(Path(dist.locate_file(f)).resolve())
    return out


proto_files = owned_files("spicedb-python-proto")
authzed_files = owned_files("authzed")

# 1. Module provenance.
import importlib  # noqa: E402

from spicedb_python_proto.bridge import CANONICAL_MODULES  # noqa: E402

for mod_path in CANONICAL_MODULES:
    mod = importlib.import_module(mod_path.replace("/", "."))
    origin = Path(mod.__file__).resolve()
    check(origin in proto_files, f"{mod.__name__} is spicedb-python-proto's file ({origin})")
    grpc_mod = mod_path + "_grpc"
    try:
        g = importlib.import_module(grpc_mod.replace("/", "."))
    except ModuleNotFoundError:
        continue
    check(Path(g.__file__).resolve() in proto_files, f"{g.__name__} is spicedb-python-proto's file")

# 2. One descriptor-pool copy, shared classes.
pool = descriptor_pool.Default()
fd = pool.FindFileByName("authzed/api/v1/core.proto")
check(fd is core_pb2.DESCRIPTOR, "pool's authzed/api/v1/core.proto is core_pb2.DESCRIPTOR")
check(
    pool.FindMessageTypeByName("authzed.api.v1.CheckPermissionRequest")
    is permission_service_pb2.CheckPermissionRequest.DESCRIPTOR,
    "pool resolves authzed.api.v1.CheckPermissionRequest to the canonical descriptor",
)
check(
    legacy_v1.CheckPermissionRequest is permission_service_pb2.CheckPermissionRequest,
    "authzed-py's CheckPermissionRequest is the canonical class",
)
check(legacy_v1.ObjectReference is core_pb2.ObjectReference, "authzed-py's ObjectReference is canonical")
check(
    legacy_materialize.WatchPermissionsRequest.DESCRIPTOR.file.pool is pool,
    "materialize messages live in the default pool",
)
check(
    v0_core_pb2.DESCRIPTOR.name == "authzed/api/v0/core.proto"
    and v1alpha1_schema_pb2.DESCRIPTOR.name == "authzed/api/v1alpha1/schema.proto",
    "authzed-py-only protos (v0, v1alpha1) still load",
)

# 3. Disjoint ownership.
overlap = sorted(str(p) for p in proto_files & authzed_files)
check(not overlap, f"distributions own disjoint files (overlap: {overlap[:5]})")
for shared_init in ("authzed/__init__.py", "authzed/api/__init__.py", "authzed/api/v1/__init__.py"):
    check(
        not any(str(p).endswith(shared_init) for p in proto_files),
        f"spicedb-python-proto does not ship {shared_init}",
    )


# 4a. Both clients against an in-process server.
class FakePermissions(permission_service_pb2_grpc.PermissionsServiceServicer):
    def __init__(self) -> None:
        self.seen: list[tuple[type, str]] = []

    def CheckPermission(self, request, context):
        md = dict(context.invocation_metadata())
        self.seen.append((type(request), md.get("authorization", "")))
        return permission_service_pb2.CheckPermissionResponse(
            permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
            checked_at=core_pb2.ZedToken(token="fake-rev"),
        )


fake = FakePermissions()
server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
permission_service_pb2_grpc.add_PermissionsServiceServicer_to_server(fake, server)
port = server.add_insecure_port("127.0.0.1:0")
server.start()
try:
    legacy = legacy_v1.InsecureClient(f"127.0.0.1:{port}", TOKEN)
    resp = legacy.CheckPermission(
        legacy_v1.CheckPermissionRequest(
            resource=legacy_v1.ObjectReference(object_type="document", object_id="d1"),
            permission="view",
            subject=legacy_v1.SubjectReference(
                object=legacy_v1.ObjectReference(object_type="user", object_id="alice")
            ),
        )
    )
    check(
        resp.permissionship == permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
        "authzed-py InsecureClient.CheckPermission round-trips through the fake server",
    )
    check(isinstance(resp, permission_service_pb2.CheckPermissionResponse), "authzed-py gets canonical responses")

    with SpiceDBClient(
        f"127.0.0.1:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT
    ) as native:
        result = native.check(
            PermissionCheck(
                resource=ObjectRef("document", "d1"),
                permission="view",
                subject=SubjectRef(ObjectRef("user", "alice")),
            ),
            consistency=consistency.full(),
        )
    check(result.has_permission, "spicedb-python check works in the same process")
    check(
        all(t is permission_service_pb2.CheckPermissionRequest for t, _ in fake.seen) and len(fake.seen) == 2,
        "both clients sent the canonical CheckPermissionRequest",
    )
    check(all(a == f"Bearer {TOKEN}" for _, a in fake.seen), "both clients sent the bearer token")
finally:
    server.stop(None)

# 4b. Live SpiceDB, when available.
endpoint = os.environ.get("SPICEDB_ENDPOINT")
if endpoint:
    legacy = legacy_v1.InsecureClient(endpoint, os.environ.get("SPICEDB_TOKEN") or TOKEN)
    legacy.WriteSchema(
        legacy_v1.WriteSchemaRequest(
            schema="definition bridgeuser {}\ndefinition bridgedoc {\n relation viewer: bridgeuser\n permission view = viewer\n}"
        )
    )
    write = legacy.WriteRelationships(
        legacy_v1.WriteRelationshipsRequest(
            updates=[
                legacy_v1.RelationshipUpdate(
                    operation=legacy_v1.RelationshipUpdate.OPERATION_TOUCH,
                    relationship=legacy_v1.Relationship(
                        resource=legacy_v1.ObjectReference(object_type="bridgedoc", object_id="d1"),
                        relation="viewer",
                        subject=legacy_v1.SubjectReference(
                            object=legacy_v1.ObjectReference(object_type="bridgeuser", object_id="alice")
                        ),
                    ),
                )
            ]
        )
    )
    with SpiceDBClient(
        endpoint, token=os.environ.get("SPICEDB_TOKEN") or TOKEN, transport_security=TransportSecurity.PLAINTEXT
    ) as native:
        result = native.check(
            PermissionCheck(
                resource=ObjectRef("bridgedoc", "d1"),
                permission="view",
                subject=SubjectRef(ObjectRef("bridgeuser", "alice")),
            ),
            consistency=consistency.at_least(ZedToken(write.written_at.token)),
        )
    check(result.has_permission, f"live: authzed-py write is visible to spicedb-python check ({endpoint})")
else:
    print("live: SPICEDB_ENDPOINT unset; live coexistence check not run")

_ = insecure_bearer_token_credentials  # grpcutil still imports
print("ALL COEXISTENCE CHECKS PASSED")
