#!/usr/bin/env python3
"""Validate portable Draft 2020-12 schemas and fixed interoperability fixtures.

Usage: python3 conformance/validate-schemas.py [MODULE_ROOT]
Requires externally supplied jsonschema (not a Cordis project dependency).
Only schemaVectors and signing byte/hash assertions execute here. codeVectors
are explicitly reported as unexecuted: the generated contract dialect, method
routing, negotiation, package policy and Ed25519 require their real Go codecs.
"""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import struct
import sys


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key!r}")
        result[key] = value
    return result


def reject_constant(value):
    raise ValueError(f"non-JSON numeric constant: {value}")


def load_json(path):
    with path.open(encoding="utf-8") as stream:
        return json.load(
            stream, object_pairs_hook=unique_object, parse_constant=reject_constant
        )


def pointer(document, path):
    if not path:
        return document
    if not path.startswith("/"):
        raise ValueError(f"invalid JSON pointer: {path!r}")
    for token in path[1:].split("/"):
        token = token.replace("~1", "/").replace("~0", "~")
        document = document[int(token)] if isinstance(document, list) else document[token]
    return document


def require(condition, message):
    if not condition:
        raise ValueError(message)


def validate_signing(vector):
    files = vector["files"]
    require(len(files) <= 10000, "too many signing entries")
    ordered = sorted(files, key=lambda item: item["path"].encode("utf-8"))
    names = [item["path"] for item in ordered]
    require(len(set(names)) == len(names), "duplicate signing path")
    require(names == vector["sortedPaths"], "UTF-8 path sorting differs")
    encoded = bytearray(b"cordis-go-package/v1\x00")
    encoded.extend(struct.pack(">I", len(ordered)))
    for item in ordered:
        content = bytes.fromhex(item["contentHex"])
        require(len(content) == item["size"], f"{item['path']}: content size differs")
        require(
            hashlib.sha256(content).hexdigest() == item["sha256"],
            f"{item['path']}: content SHA-256 differs",
        )
        path = item["path"].encode("utf-8")
        encoded.extend(struct.pack(">I", len(path)))
        encoded.extend(path)
        encoded.extend(struct.pack(">Q", item["size"]))
        encoded.extend(bytes.fromhex(item["sha256"]))
    expected = bytes.fromhex(vector["inputHex"])
    require(bytes(encoded) == expected, "canonical input differs from fixed inputHex")
    require(len(expected) == vector["inputBytes"], "fixed input length differs")
    require(
        hashlib.sha256(expected).hexdigest() == vector["digestSHA256"],
        "fixed input SHA-256 differs",
    )
    ed25519 = vector["ed25519"]
    require(len(bytes.fromhex(ed25519["seedHex"])) == 32, "Ed25519 seed must be 32 bytes")
    require(
        len(bytes.fromhex(ed25519["publicKeyHex"])) == 32,
        "Ed25519 public key must be 32 bytes",
    )
    signature = base64.b64decode(ed25519["signatureBase64"], validate=True)
    require(len(signature) == 64, "Ed25519 signature must be 64 bytes")
    require(
        base64.b64encode(signature).decode("ascii") == ed25519["signatureBase64"],
        "Ed25519 signature must use canonical padded base64",
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "module_root", nargs="?", type=Path,
        default=Path(__file__).resolve().parent.parent,
    )
    args = parser.parse_args()
    try:
        from jsonschema import Draft202012Validator
    except ImportError:
        parser.exit(
            2,
            "error: external Python package jsonschema is required; supply it in "
            "your conformance environment (no project dependency is added).\n",
        )

    root = args.module_root.resolve()
    try:
        fixture = load_json(root / "schema/testdata/interoperability.json")
        require(fixture["schema"] == "cordis-go.interoperability/v1", "unknown fixture version")
        schemas = {}
        for relative in fixture["schemaFiles"]:
            document = load_json(root / relative)
            require(
                document.get("$schema") == "https://json-schema.org/draft/2020-12/schema",
                f"{relative}: expected Draft 2020-12 declaration",
            )
            Draft202012Validator.check_schema(document)
            schemas[relative] = document
            print(f"PASS metaschema: {relative}")
        require(len(schemas) == 4, "expected four distinct portable envelope schemas")
        seen = set()
        for vector in fixture["schemaVectors"]:
            name = vector["name"]
            require(name not in seen, f"duplicate vector name: {name}")
            seen.add(name)
            require(vector["validationLevel"] == "schema", f"{name}: wrong validation level")
            require(type(vector["accept"]) is bool, f"{name}: accept must be boolean")
            document = schemas[vector["schema"]]
            selected = pointer(document, vector.get("pointer", ""))
            # No format/contentEncoding assertion extensions: those are code-level
            # guarantees, not structural Draft 2020-12 guarantees.
            validator = Draft202012Validator(document).evolve(schema=selected)
            errors = list(validator.iter_errors(vector["instance"]))
            actual = not errors
            if actual != vector["accept"]:
                detail = errors[0].message if errors else "instance was unexpectedly accepted"
                raise ValueError(f"{name}: expected accept={vector['accept']}; {detail}")
            print(f"PASS schema vector: {name} ({'accept' if actual else 'reject'})")
        for vector in fixture["signingVectors"]:
            validate_signing(vector)
            print(f"PASS signing bytes/SHA-256: {vector['name']}")
        # These metadata checks do not execute or reinterpret the real codecs.
        for vector in fixture["codeVectors"]:
            require(vector["validationLevel"] == "code", "wrong code vector validation level")
            require(type(vector["accept"]) is bool, f"{vector['name']}: accept must be boolean")
            require((root / vector["source"]).is_file(), f"missing code source: {vector['source']}")
            if "contractSchema" in vector:
                pointer(load_json(root / vector["contractSchema"]), vector["pointer"])
        print(
            f"Validated {len(schemas)} metaschemas, {len(fixture['schemaVectors'])} structural "
            f"vectors and {len(fixture['signingVectors'])} fixed signing byte/hash vector(s)."
        )
        print(
            f"NOT EXECUTED: {len(fixture['codeVectors'])} code-level vectors and Ed25519 "
            "verification; run the real Go codec/package conformance checks separately."
        )
    except (OSError, ValueError, KeyError, IndexError, TypeError, struct.error) as error:
        print(f"FAIL conformance: {error}", file=sys.stderr)
        return 1
    except Exception as error:
        # Includes jsonschema.SchemaError while preserving a useful CLI failure.
        print(f"FAIL schema validation: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
