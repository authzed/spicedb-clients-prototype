"""Uses the --python.names-only output (names.py, beside this file).

`mage pythonTypeErrors` runs this with `python3 -I -S`: no site-packages and
no environment, so it passes only while names.py imports the standard library
alone and a shared API package can use the schema's names without depending
on the SpiceDB client. It then type-checks names.py with mypy --strict.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import names  # noqa: E402

assert "spicedb" not in sys.modules and "grpc" not in sys.modules
assert names.DefinitionNames.DOCUMENT == "document"
assert names.DocumentRelations.VIEWER == "viewer"
assert names.DocumentPermissions.VIEW == "view"
assert names.DOCUMENT_PERMISSION_NAMES == ("view", "edit", "delete")
assert names.TEAM_RELATION_NAMES == ("member",)
assert names.DEFINITION_NAMES == ("user", "team", "document", "organization", "invitation")
assert "definition document {" in names.SCHEMA_TEXT
with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "sample.zed")) as f:
    assert names.SCHEMA_TEXT == f.read()
print("names-only output loads with the standard library alone")
