"""Bridge authzed-py onto spicedb-python-proto's canonical protocol bindings.

Why this exists
---------------
authzed-py (the ``authzed`` distribution on PyPI) and spicedb-python-proto both
install generated modules at the same import paths: ``authzed/api/v1/core_pb2.py``,
``buf/validate/validate_pb2.py``, ``validate/validate_pb2.py`` and so on. Installed
side by side they overwrite each other's files in ``site-packages``, so whichever
installed last wins, a process can end up with modules generated from two
different API revisions (the default descriptor pool then rejects the second,
different ``authzed/api/v1/core.proto``, or a message silently lacks a field),
and uninstalling either distribution deletes files the other one needs.

Root DESIGN.md ("Be the single copy of the protocol bindings in any process")
makes this distribution the canonical binding. authzed-py migrates by
re-exporting it rather than shipping its own copy. This tool performs that
rewrite on an authzed-py checkout, sdist or wheel:

* It deletes every generated module this distribution provides (``X_pb2.py``,
  ``X_pb2.pyi``, ``X_pb2_grpc.py``, ``X_pb2_grpc.pyi`` for each module in
  :data:`CANONICAL_MODULES`) and authzed-py's ``authzed/py.typed`` marker, which
  this distribution also ships. Generated modules authzed-py has and this
  distribution does not (``authzed/api/v0``, ``authzed/api/v1alpha1``) are kept;
  they import the canonical modules by their unchanged import paths.
* It keeps authzed-py's hand-written code (the client classes in
  ``authzed/api/v1/__init__.py`` and ``authzed/api/materialize/v0/__init__.py``,
  and ``grpcutil``), and makes each such ``__init__.py`` extend its ``__path__``
  with :func:`pkgutil.extend_path`, so the canonical modules resolve even when
  the two distributions live on different ``sys.path`` entries (editable installs,
  ``PYTHONPATH`` layouts, zipapps). In one ``site-packages`` directory that is a
  no-op.
* It adds a ``spicedb-python-proto`` requirement, and drops build-backend module
  entries (``buf``, ``validate``, ``protoc_gen_openapiv2``) that no longer exist.

This distribution never ships an ``__init__.py`` for a package authzed-py also
populates (``authzed``, ``authzed.api``, ``authzed.api.v1``,
``authzed.api.materialize.v0``, ``buf``, ``validate``, ...), so after the rewrite
the two distributions own disjoint sets of files.

Usage::

    authzed-py-bridge --root path/to/authzed-py            # checkout, in place
    authzed-py-bridge --sdist authzed-1.25.0.tar.gz --out dist/
    authzed-py-bridge --wheel authzed-1.25.0-py3-none-any.whl --out dist/

The input is treated as data: nothing in it is imported or executed.
"""

from __future__ import annotations

import argparse
import ast
import base64
import csv
import hashlib
import io
import re
import sys
import tarfile
import tempfile
import zipfile
from collections.abc import Iterable
from pathlib import Path, PurePosixPath

# Every generated module this distribution ships, as slash-separated paths
# without the ``.py`` suffix. tests/test_bridge.py asserts this matches gen/
# exactly, so a regenerated protocol that adds a file cannot silently leave
# authzed-py's stale copy of it in place.
CANONICAL_MODULES: tuple[str, ...] = (
    "authzed/api/materialize/v0/relationships_pb2",
    "authzed/api/materialize/v0/roaringlookupresources_pb2",
    "authzed/api/materialize/v0/watchpermissions_pb2",
    "authzed/api/materialize/v0/watchpermissionsets_pb2",
    "authzed/api/v1/core_pb2",
    "authzed/api/v1/debug_pb2",
    "authzed/api/v1/error_reason_pb2",
    "authzed/api/v1/experimental_service_pb2",
    "authzed/api/v1/openapi_pb2",
    "authzed/api/v1/permission_service_pb2",
    "authzed/api/v1/schema_service_pb2",
    "authzed/api/v1/watch_service_pb2",
    "buf/validate/validate_pb2",
    "protoc_gen_openapiv2/options/annotations_pb2",
    "protoc_gen_openapiv2/options/openapiv2_pb2",
    "validate/validate_pb2",
)

# Other files this distribution owns that authzed-py also ships.
CANONICAL_EXTRA_FILES: tuple[str, ...] = ("authzed/py.typed",)

DEFAULT_REQUIREMENT_NAME = "spicedb-python-proto"

MARKER = "spicedb-python-proto bridge"
_EXTEND_PATH = (
    f"# Added by the {MARKER}: the generated *_pb2 modules of this package are\n"
    "# provided by spicedb-python-proto, which may live on another sys.path entry.\n"
    '__path__ = __import__("pkgutil").extend_path(__path__, __name__)\n'
)


def bridged_files() -> frozenset[str]:
    """Return every package-relative file path the bridge removes."""
    out: set[str] = set(CANONICAL_EXTRA_FILES)
    for mod in CANONICAL_MODULES:
        for suffix in (".py", ".pyi", "_grpc.py", "_grpc.pyi"):
            out.add(mod + suffix)
    return frozenset(out)


def is_bridged(rel_path: str) -> bool:
    """Report whether a package-relative path is a file the bridge removes."""
    return PurePosixPath(rel_path).as_posix() in bridged_files()


def default_requirement() -> str:
    try:
        from importlib.metadata import version

        return f"{DEFAULT_REQUIREMENT_NAME}>={version(DEFAULT_REQUIREMENT_NAME)}"
    except Exception:  # running from a source tree without installed metadata
        return DEFAULT_REQUIREMENT_NAME


def add_extend_path(source: str) -> str:
    """Insert the ``pkgutil.extend_path`` line into an ``__init__`` module.

    It goes after the module docstring and any ``from __future__`` imports
    (which must stay first), before every other statement. Idempotent.
    """
    if MARKER in source:
        return source
    tree = ast.parse(source)
    insert_line = 0  # 0-based line index to insert before
    for i, node in enumerate(tree.body):
        is_doc = (
            i == 0
            and isinstance(node, ast.Expr)
            and isinstance(node.value, ast.Constant)
            and isinstance(node.value.value, str)
        )
        is_future = isinstance(node, ast.ImportFrom) and node.module == "__future__"
        if is_doc or is_future:
            insert_line = node.end_lineno or node.lineno
            continue
        break
    lines = source.splitlines(keepends=True)
    if lines and insert_line and not lines[insert_line - 1].endswith("\n"):
        lines[insert_line - 1] += "\n"
    lines.insert(insert_line, _EXTEND_PATH + "\n")
    return "".join(lines)


def _top_level_dirs_left(pkg_root: Path, names: Iterable[str]) -> list[str]:
    return [n for n in names if (pkg_root / n).exists() or (pkg_root / f"{n}.py").exists()]


def _find_array(key: str, text: str) -> tuple[int, int] | None:
    """Locate the array assigned to ``key`` at the start of a line in ``text``.

    Returns ``(start, end)`` such that ``text[start:end]`` is everything between
    the array's brackets, or None when ``key`` is not assigned. The closing bracket is found by a bracket-aware scan that skips TOML string
    literals (basic, literal and their multi-line forms) and comments, so a
    ``]`` inside a value such as ``"grpcio[protobuf]>=1.63"`` does not end it.
    """
    m = re.search(rf"(?m)^{re.escape(key)}\s*=\s*\[", text)
    if m is None:
        return None
    i, depth = m.end(), 1
    while i < len(text):
        c = text[i]
        if c in "\"'":
            quote = text[i : i + 3] if text[i : i + 3] in ('"""', "'''") else c
            i += len(quote)
            while i < len(text) and not text.startswith(quote, i):
                # Only basic (double-quoted) strings have escapes.
                i += 2 if quote[0] == '"' and text[i] == "\\" else 1
            if len(quote) == 3:
                # A multi-line string may end with up to two extra quotes.
                while text.startswith(quote[0], i + 3):
                    i += 1
            i += len(quote)
            continue
        if c == "#":
            nl = text.find("\n", i)
            i = len(text) if nl < 0 else nl
            continue
        if c == "[":
            depth += 1
        elif c == "]":
            depth -= 1
            if depth == 0:
                return m.end(), i
        i += 1
    raise SystemExit(f"pyproject.toml: the {key} array is never closed")


def _rewrite_pyproject(text: str, requirement: str, pkg_root: Path) -> str:
    """Add the requirement and drop build-backend module names that are gone.

    Edits are textual so formatting and comments survive; only the
    ``[project] dependencies`` array and uv_build's ``module-name`` array are
    touched.
    """
    req_name = re.split(r"[<>=!~;\[ ]", requirement, maxsplit=1)[0]
    project = re.search(r"(?ms)^\[project\]\s*$.*?(?=^\[|\Z)", text)
    if project is None:
        raise SystemExit("pyproject.toml has no [project] table; cannot add the requirement")
    section = project.group(0)
    deps = _find_array("dependencies", section)
    if deps is None:
        raise SystemExit("[project] has no dependencies array; cannot add the requirement")
    deps_start, deps_end = deps
    body = section[deps_start:deps_end]
    if re.search(rf'["\']{re.escape(req_name)}\s*[<>=!~;\["\']', body) is None:
        indent = "  "
        m = re.search(r"\n([ \t]+)\S", body)
        if m:
            indent = m.group(1)
        new_body = body.rstrip()
        if new_body and not new_body.endswith(","):
            new_body += ","
        new_body += f'\n{indent}"{requirement}",\n'
        section = section[:deps_start] + new_body + section[deps_end:]
        text = text[: project.start()] + section + text[project.end() :]

    mod = _find_array("module-name", text)
    if mod is not None:
        mod_start, mod_end = mod
        body = text[mod_start:mod_end]
        items = re.findall(r'["\']([^"\']+)["\']', body)
        kept = _top_level_dirs_left(pkg_root, items)
        if kept != items:
            for name in items:
                if name not in kept:
                    body = re.sub(rf'\n?[ \t]*["\']{re.escape(name)}["\'],?[ \t]*', "", body)
            text = text[:mod_start] + body + text[mod_end:]
    return text


def _prune_empty_dirs(root: Path) -> None:
    for d in sorted((p for p in root.rglob("*") if p.is_dir()), key=lambda p: len(p.parts), reverse=True):
        if not any(d.iterdir()):
            d.rmdir()


def bridge_tree(root: Path, requirement: str | None = None) -> list[str]:
    """Rewrite an authzed-py checkout or unpacked sdist in place.

    Returns the package-relative paths removed. Package sources are found under
    ``src/`` when present, otherwise at ``root``.
    """
    requirement = requirement or default_requirement()
    pkg_root = root / "src" if (root / "src" / "authzed").is_dir() else root
    if not (pkg_root / "authzed").is_dir():
        raise SystemExit(f"{root}: no authzed package found (looked in src/ and the root)")

    removed: list[str] = []
    touched_dirs: set[Path] = set()
    for rel in sorted(bridged_files()):
        path = pkg_root / rel
        if path.is_file():
            path.unlink()
            removed.append(rel)
            touched_dirs.add(path.parent)

    for d in sorted(touched_dirs):
        init = d / "__init__.py"
        if init.is_file():
            init.write_text(add_extend_path(init.read_text()))

    # Drop the now-empty package directories (buf/, validate/, ...) but never
    # a directory that still has hand-written content.
    for top in ("buf", "validate", "protoc_gen_openapiv2", "authzed"):
        if (pkg_root / top).is_dir():
            _prune_empty_dirs(pkg_root / top)
            if not any((pkg_root / top).iterdir()):
                (pkg_root / top).rmdir()

    pyproject = root / "pyproject.toml"
    if pyproject.is_file():
        pyproject.write_text(_rewrite_pyproject(pyproject.read_text(), requirement, pkg_root))
    pkg_info = root / "PKG-INFO"
    if pkg_info.is_file():
        pkg_info.write_text(_add_requires_dist(pkg_info.read_text(), requirement))
    return removed


def _add_requires_dist(metadata: str, requirement: str) -> str:
    req_name = re.split(r"[<>=!~;\[ ]", requirement, maxsplit=1)[0]
    if re.search(rf"(?mi)^Requires-Dist:\s*{re.escape(req_name)}\b", metadata):
        return metadata
    header, sep, body = metadata.partition("\n\n")
    header = header.rstrip("\n") + f"\nRequires-Dist: {requirement}"
    return header + (sep + body if sep else "\n")


def bridge_sdist(sdist: Path, out_dir: Path, requirement: str | None = None) -> Path:
    """Rewrite an authzed-py sdist (``.tar.gz``) into ``out_dir``."""
    out_dir.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        with tarfile.open(sdist) as tf:
            tf.extractall(tmp_path, filter="data")
        tops = [p for p in tmp_path.iterdir() if p.is_dir()]
        if len(tops) != 1:
            raise SystemExit(f"{sdist}: expected one top-level directory, found {len(tops)}")
        bridge_tree(tops[0], requirement)
        target = out_dir / sdist.name
        with tarfile.open(target, "w:gz") as tf:
            tf.add(tops[0], arcname=tops[0].name)
    return target


def _record_hash(data: bytes) -> str:
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=")
    return "sha256=" + digest.decode()


def bridge_wheel(wheel: Path, out_dir: Path, requirement: str | None = None) -> Path:
    """Rewrite an authzed-py wheel into ``out_dir`` (same file name)."""
    requirement = requirement or default_requirement()
    out_dir.mkdir(parents=True, exist_ok=True)
    target = out_dir / wheel.name
    if target.resolve() == wheel.resolve():
        raise SystemExit("--out must differ from the input wheel's directory")

    with zipfile.ZipFile(wheel) as zin:
        names = zin.namelist()
        dist_info = {n.split("/", 1)[0] for n in names if n.split("/", 1)[0].endswith(".dist-info")}
        if len(dist_info) != 1:
            raise SystemExit(f"{wheel}: expected one .dist-info directory")
        (dist_info_dir,) = dist_info
        record_name = f"{dist_info_dir}/RECORD"

        kept: list[str] = [n for n in names if not n.endswith("/") and not is_bridged(n)]
        dropped_dirs = {str(PurePosixPath(n).parent) for n in names if is_bridged(n)}
        contents: dict[str, bytes] = {}
        for name in kept:
            if name == record_name:
                continue
            data = zin.read(name)
            if name == f"{dist_info_dir}/METADATA":
                data = _add_requires_dist(data.decode(), requirement).encode()
            elif PurePosixPath(name).name == "__init__.py" and str(PurePosixPath(name).parent) in dropped_dirs:
                data = add_extend_path(data.decode()).encode()
            contents[name] = data

    rows = [(name, _record_hash(data), str(len(data))) for name, data in contents.items()]
    rows.append((record_name, "", ""))
    buf = io.StringIO()
    csv.writer(buf, lineterminator="\n").writerows(rows)
    contents[record_name] = buf.getvalue().encode()

    with zipfile.ZipFile(target, "w", compression=zipfile.ZIP_DEFLATED) as zout:
        # dist-info last, RECORD very last, as the wheel spec recommends.
        order = sorted(contents, key=lambda n: (n.startswith(dist_info_dir), n == record_name, n))
        for name in order:
            zout.writestr(name, contents[name])
    return target


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="authzed-py-bridge",
        description="Rewrite authzed-py to re-export spicedb-python-proto's canonical bindings.",
    )
    src = parser.add_mutually_exclusive_group(required=True)
    src.add_argument("--root", type=Path, help="authzed-py checkout to rewrite in place")
    src.add_argument("--sdist", type=Path, help="authzed-py .tar.gz sdist to rewrite into --out")
    src.add_argument("--wheel", type=Path, help="authzed-py wheel to rewrite into --out")
    parser.add_argument("--out", type=Path, help="output directory for --sdist/--wheel")
    parser.add_argument(
        "--requirement",
        help="requirement to add (default: spicedb-python-proto>=<installed version>)",
    )
    args = parser.parse_args(argv)

    if args.root is not None:
        removed = bridge_tree(args.root, args.requirement)
        print(f"bridged {args.root}: removed {len(removed)} generated files")
        return 0
    if args.out is None:
        parser.error("--out is required with --sdist/--wheel")
    if args.sdist is not None:
        print(f"wrote {bridge_sdist(args.sdist, args.out, args.requirement)}")
    else:
        print(f"wrote {bridge_wheel(args.wheel, args.out, args.requirement)}")
    return 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())

__all__ = [
    "CANONICAL_MODULES",
    "CANONICAL_EXTRA_FILES",
    "add_extend_path",
    "bridge_sdist",
    "bridge_tree",
    "bridge_wheel",
    "bridged_files",
    "is_bridged",
    "main",
]
