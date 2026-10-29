"""Tests for the authzed-py bridge and the coexistence layout (root DESIGN.md,
"Be the single copy of the protocol bindings in any process").

The coexistence test downloads a pinned authzed-py wheel from PyPI (or uses
AUTHZED_PY_WHEEL), bridges it, installs it into a fresh venv together with this
distribution and spicedb-python, and runs bridge_coexistence_check.py there. It
needs `uv` and network access (or a warm uv cache) and FAILS rather than skips
without them: a skipped coexistence test reads as coverage while proving
nothing. Set SPICEDB_ENDPOINT to also exercise both clients against a live
SpiceDB.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
import urllib.request
import zipfile
from pathlib import Path

import pytest

from spicedb_python_proto import bridge

TIER = Path(__file__).resolve().parent.parent
GEN = TIER / "gen"
NATIVE = TIER.parent.parent / "spicedb-python"
AUTHZED_PY_VERSION = os.environ.get("AUTHZED_PY_VERSION", "1.25.0")


# --- layout invariants -------------------------------------------------------


def test_canonical_modules_match_generated_tree():
    on_disk = {
        str(p.relative_to(GEN).with_suffix("")).replace(os.sep, "/")
        for p in GEN.rglob("*_pb2.py")
        if p.relative_to(GEN).parts[0] != "google"  # shipped by googleapis-common-protos
    }
    assert on_disk == set(bridge.CANONICAL_MODULES)


def test_generated_tree_has_no_package_init_files():
    # authzed-py owns authzed/api/v1/__init__.py (its client classes); this tier
    # shipping any __init__.py in a shared package would collide with it.
    assert sorted(GEN.rglob("__init__.py")) == []


def test_every_generated_module_has_a_type_stub():
    for mod in bridge.CANONICAL_MODULES:
        assert (GEN / f"{mod}.pyi").is_file(), mod


@pytest.fixture(scope="session")
def proto_wheel(tmp_path_factory) -> Path:
    out = tmp_path_factory.mktemp("proto-wheel")
    subprocess.run(["uv", "build", "--wheel", "-o", str(out), str(TIER)], check=True, capture_output=True)
    (whl,) = out.glob("spicedb_python_proto-*.whl")
    return whl


def test_wheel_ships_no_shared_init_and_owns_typing_marker(proto_wheel):
    names = set(zipfile.ZipFile(proto_wheel).namelist())
    for shared in (
        "authzed/__init__.py",
        "authzed/api/__init__.py",
        "authzed/api/v1/__init__.py",
        "authzed/api/materialize/__init__.py",
        "authzed/api/materialize/v0/__init__.py",
        "buf/__init__.py",
        "validate/__init__.py",
    ):
        assert shared not in names
    assert "authzed/py.typed" in names
    assert "spicedb_python_proto/bridge.py" in names
    for mod in bridge.CANONICAL_MODULES:
        assert f"{mod}.py" in names and f"{mod}.pyi" in names, mod
    entry_points = zipfile.ZipFile(proto_wheel).read(
        next(n for n in names if n.endswith(".dist-info/entry_points.txt"))
    ).decode()
    assert "authzed-py-bridge = spicedb_python_proto.bridge:main" in entry_points


# --- bridge unit tests on a synthetic authzed-py checkout --------------------

PYPROJECT = """\
[project]
name = "authzed"
version = "9.9.9"
dependencies = [
  "grpcio~=1.63",
  "protobuf>=5.26,<8",
]

[tool.uv.build-backend]
module-name = [
  "authzed",
  "buf",
  "grpcutil",
  "protoc_gen_openapiv2",
  "validate",
]
namespace = true
"""


def make_checkout(root: Path) -> Path:
    src = root / "src"
    files = {
        "authzed/py.typed": "",
        "authzed/api/v1/__init__.py": '"""Legacy client."""\nfrom __future__ import annotations\n\nimport grpc\n',
        "authzed/api/v1/__init__.pyi": "",
        "authzed/api/v1/core_pb2.py": "",
        "authzed/api/v1/core_pb2.pyi": "",
        "authzed/api/v1/core_pb2_grpc.py": "",
        "authzed/api/v1/core_pb2_grpc.pyi": "",
        "authzed/api/v1/permission_service_pb2.py": "",
        "authzed/api/materialize/v0/__init__.py": "import grpc\n",
        "authzed/api/materialize/v0/watchpermissions_pb2.py": "",
        "authzed/api/v0/core_pb2.py": "KEEP",
        "authzed/api/v1alpha1/schema_pb2.py": "KEEP",
        "buf/validate/validate_pb2.py": "",
        "buf/validate/validate_pb2_grpc.py": "",
        "validate/validate_pb2.py": "",
        "protoc_gen_openapiv2/options/annotations_pb2.py": "",
        "protoc_gen_openapiv2/options/openapiv2_pb2.py": "",
        "grpcutil/__init__.py": "KEEP",
    }
    for rel, body in files.items():
        (src / rel).parent.mkdir(parents=True, exist_ok=True)
        (src / rel).write_text(body)
    (root / "pyproject.toml").write_text(PYPROJECT)
    return src


def test_bridge_tree_drops_canonical_modules_and_keeps_the_rest(tmp_path):
    src = make_checkout(tmp_path)
    removed = bridge.bridge_tree(tmp_path, "spicedb-python-proto>=0.1.0")

    assert "authzed/api/v1/core_pb2.py" in removed
    for gone in (
        "authzed/py.typed",
        "authzed/api/v1/core_pb2.py",
        "authzed/api/v1/core_pb2_grpc.pyi",
        "authzed/api/materialize/v0/watchpermissions_pb2.py",
        "buf",
        "validate",
        "protoc_gen_openapiv2",
    ):
        assert not (src / gone).exists(), gone
    for kept in ("authzed/api/v0/core_pb2.py", "authzed/api/v1alpha1/schema_pb2.py", "grpcutil/__init__.py"):
        assert (src / kept).read_text() == "KEEP"

    init = (src / "authzed/api/v1/__init__.py").read_text()
    # Docstring and __future__ import stay first; extend_path precedes the rest.
    assert init.index('"""Legacy client."""') < init.index("from __future__") < init.index("extend_path")
    assert init.index("extend_path") < init.index("import grpc")
    compile(init, "__init__.py", "exec")
    assert "extend_path" in (src / "authzed/api/materialize/v0/__init__.py").read_text()

    pyproject = (tmp_path / "pyproject.toml").read_text()
    assert '"spicedb-python-proto>=0.1.0",' in pyproject
    import tomllib

    data = tomllib.loads(pyproject)
    assert data["project"]["dependencies"][-1] == "spicedb-python-proto>=0.1.0"
    assert data["tool"]["uv"]["build-backend"]["module-name"] == ["authzed", "grpcutil"]


@pytest.mark.parametrize(
    "deps",
    [
        '["grpcio[protobuf]>=1.63", "protobuf>=5"]',
        '[\n  "grpcio[protobuf]>=1.63",  # extras contain ] too\n  \'typing-extensions\', "protobuf>=5"\n]',
    ],
)
def test_rewrite_pyproject_keeps_dependencies_with_extras(tmp_path, deps):
    # A dependency's extras ("pkg[extra]") contain "]"; the array ends at the
    # bracket that closes it, not at the first "]" inside a string or comment.
    import tomllib

    text = f'[project]\nname = "authzed"\ndependencies = {deps}\n\n[tool.other]\nkeep = ["x"]\n'
    before = tomllib.loads(text)["project"]["dependencies"]
    out = bridge._rewrite_pyproject(text, "spicedb-python-proto>=0.1.0", tmp_path)
    data = tomllib.loads(out)
    assert data["project"]["dependencies"] == [*before, "spicedb-python-proto>=0.1.0"]
    assert data["tool"]["other"] == {"keep": ["x"]}
    # Only the array changed; everything around it is byte-for-byte intact.
    start, end = text.index("dependencies"), text.index("\n\n[tool.other]")
    assert out.startswith(text[:start]) and out.endswith(text[end:])
    assert bridge._rewrite_pyproject(out, "spicedb-python-proto>=0.1.0", tmp_path) == out


def test_bridge_tree_is_idempotent(tmp_path):
    make_checkout(tmp_path)
    bridge.bridge_tree(tmp_path, "spicedb-python-proto>=0.1.0")
    first = {p: p.read_bytes() for p in tmp_path.rglob("*") if p.is_file()}
    assert bridge.bridge_tree(tmp_path, "spicedb-python-proto>=0.1.0") == []
    assert {p: p.read_bytes() for p in tmp_path.rglob("*") if p.is_file()} == first


def test_extend_path_makes_split_layout_resolve(tmp_path):
    # The editable/PYTHONPATH layout: legacy __init__.py on one sys.path entry,
    # canonical modules on another. Without extend_path the regular package
    # authzed.api.v1 would only search its own directory.
    legacy, canonical = tmp_path / "legacy", tmp_path / "canonical"
    (legacy / "pkgx/api/v1").mkdir(parents=True)
    (legacy / "pkgx/api/v1/__init__.py").write_text(bridge.add_extend_path("from pkgx.api.v1.core_pb2 import X\n"))
    (canonical / "pkgx/api/v1").mkdir(parents=True)
    (canonical / "pkgx/api/v1/core_pb2.py").write_text("X = 'canonical'\n")
    out = subprocess.run(
        [sys.executable, "-I", "-c", "import sys; sys.path[:0] = sys.argv[1:]; import pkgx.api.v1 as m; print(m.X)",
         str(legacy), str(canonical)],
        capture_output=True, text=True, check=True,
    )
    assert out.stdout.strip() == "canonical"


def test_bridge_wheel_rewrites_record_and_metadata(tmp_path):
    whl = tmp_path / "in" / "authzed-9.9.9-py3-none-any.whl"
    whl.parent.mkdir()
    with zipfile.ZipFile(whl, "w") as z:
        z.writestr("authzed/api/v1/__init__.py", "import grpc\n")
        z.writestr("authzed/api/v1/core_pb2.py", "")
        z.writestr("authzed/api/v0/core_pb2.py", "KEEP")
        z.writestr("authzed/py.typed", "")
        z.writestr("validate/validate_pb2.py", "")
        z.writestr("authzed-9.9.9.dist-info/METADATA", "Metadata-Version: 2.4\nName: authzed\n\nREADME\n")
        z.writestr("authzed-9.9.9.dist-info/RECORD", "stale\n")
    out = bridge.bridge_wheel(whl, tmp_path / "out", "spicedb-python-proto>=0.1.0")
    z = zipfile.ZipFile(out)
    names = set(z.namelist())
    assert names == {
        "authzed/api/v1/__init__.py",
        "authzed/api/v0/core_pb2.py",
        "authzed-9.9.9.dist-info/METADATA",
        "authzed-9.9.9.dist-info/RECORD",
    }
    meta = z.read("authzed-9.9.9.dist-info/METADATA").decode()
    assert "Requires-Dist: spicedb-python-proto>=0.1.0\n\nREADME" in meta
    record = z.read("authzed-9.9.9.dist-info/RECORD").decode().splitlines()
    assert len(record) == 4 and "stale" not in record
    import base64

    for line in record:
        name, digest, size = line.split(",")
        if name.endswith("RECORD"):
            continue
        data = z.read(name)
        expect = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
        assert digest == f"sha256={expect}" and int(size) == len(data)
    assert "extend_path" in z.read("authzed/api/v1/__init__.py").decode()


def test_bridge_sdist_rewrites_the_archive(tmp_path):
    import tarfile

    checkout = tmp_path / "authzed-9.9.9"
    make_checkout(checkout)
    (checkout / "PKG-INFO").write_text("Metadata-Version: 2.4\nName: authzed\nVersion: 9.9.9\n\nREADME\n")
    sdist = tmp_path / "authzed-9.9.9.tar.gz"
    with tarfile.open(sdist, "w:gz") as tf:
        tf.add(checkout, arcname=checkout.name)
    out = bridge.bridge_sdist(sdist, tmp_path / "out", "spicedb-python-proto>=0.1.0")
    with tarfile.open(out) as tf:
        names = set(tf.getnames())
        pkg_info = tf.extractfile("authzed-9.9.9/PKG-INFO").read().decode()
    assert "authzed-9.9.9/src/authzed/api/v1/core_pb2.py" not in names
    assert "authzed-9.9.9/src/authzed/api/v0/core_pb2.py" in names
    assert "authzed-9.9.9/src/buf" not in names
    assert "Requires-Dist: spicedb-python-proto>=0.1.0" in pkg_info


# --- coexistence with the real authzed-py ------------------------------------


def fetch_authzed_wheel(dest: Path) -> Path:
    override = os.environ.get("AUTHZED_PY_WHEEL")
    if override:
        return Path(override)
    with urllib.request.urlopen(f"https://pypi.org/pypi/authzed/{AUTHZED_PY_VERSION}/json", timeout=60) as r:
        meta = json.load(r)
    (entry,) = [u for u in meta["urls"] if u["packagetype"] == "bdist_wheel"]
    target = dest / entry["filename"]
    with urllib.request.urlopen(entry["url"], timeout=120) as r:
        data = r.read()
    assert hashlib.sha256(data).hexdigest() == entry["digests"]["sha256"]
    target.write_bytes(data)
    return target


@pytest.fixture(scope="session")
def authzed_wheels(tmp_path_factory) -> tuple[Path, Path]:
    raw = fetch_authzed_wheel(tmp_path_factory.mktemp("authzed-raw"))
    bridged = bridge.bridge_wheel(raw, tmp_path_factory.mktemp("authzed-bridged"), "spicedb-python-proto")
    return raw, bridged


def test_unbridged_authzed_py_collides_with_this_tier(authzed_wheels, proto_wheel):
    # The negative control: this is the overwrite hazard the bridge removes.
    raw, _ = authzed_wheels
    ours = {n for n in zipfile.ZipFile(proto_wheel).namelist() if ".dist-info/" not in n}
    theirs = {n for n in zipfile.ZipFile(raw).namelist() if ".dist-info/" not in n}
    assert "authzed/api/v1/core_pb2.py" in ours & theirs


def test_bridged_authzed_py_owns_no_file_of_this_tier(authzed_wheels, proto_wheel):
    _, bridged = authzed_wheels
    ours = {n for n in zipfile.ZipFile(proto_wheel).namelist() if ".dist-info/" not in n}
    theirs = {n for n in zipfile.ZipFile(bridged).namelist() if ".dist-info/" not in n}
    assert ours & theirs == set()


def test_bridged_authzed_py_coexists_in_one_interpreter(authzed_wheels, proto_wheel, tmp_path):
    uv = shutil.which("uv")
    assert uv, "uv is required for the coexistence test"
    _, bridged = authzed_wheels
    native_out = tmp_path / "native"
    subprocess.run([uv, "build", "--wheel", "-o", str(native_out), str(NATIVE)], check=True, capture_output=True)
    (native_whl,) = native_out.glob("spicedb_python-*.whl")

    venv = tmp_path / "venv"
    subprocess.run([uv, "venv", "--python", sys.executable, str(venv)], check=True, capture_output=True)
    py = venv / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
    install = subprocess.run(
        [uv, "pip", "install", "--python", str(py), str(proto_wheel), str(native_whl), str(bridged)],
        capture_output=True, text=True,
    )
    assert install.returncode == 0, install.stderr

    script = tmp_path / "check.py"
    shutil.copy(Path(__file__).with_name("bridge_coexistence_check.py"), script)
    env = {k: v for k, v in os.environ.items() if not k.startswith("PYTHON")}
    run = subprocess.run([str(py), "-I", str(script)], capture_output=True, text=True, env=env, cwd=tmp_path)
    print(run.stdout)
    assert run.returncode == 0, run.stdout + run.stderr
    assert "ALL COEXISTENCE CHECKS PASSED" in run.stdout

    # Uninstalling the legacy library must leave the canonical modules intact.
    subprocess.run([uv, "pip", "uninstall", "--python", str(py), "authzed"], check=True, capture_output=True)
    after = subprocess.run(
        [str(py), "-I", "-c", "from authzed.api.v1 import core_pb2, permission_service_pb2_grpc; print('ok')"],
        capture_output=True, text=True, cwd=tmp_path,
    )
    assert after.stdout.strip() == "ok", after.stderr
