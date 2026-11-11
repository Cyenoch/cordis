import json
import sys
from jsonschema import Draft202012Validator

with open(sys.argv[1], encoding="utf-8") as stream:
    vectors = json.load(stream)
count = 0
for vector in vectors:
    Draft202012Validator.check_schema(vector["schema"])
    validator = Draft202012Validator(vector["schema"])
    for value in vector["valid"]:
        validator.validate(value)
        count += 1
    for value in vector["invalid"]:
        assert not validator.is_valid(value), value
        count += 1
with open(sys.argv[2], encoding="utf-8") as stream:
    declaration_schema = json.load(stream)
Draft202012Validator.check_schema(declaration_schema)
validator = Draft202012Validator(declaration_schema)
# A hand-written declaration, and one derived from Go source.
for path in ["testdata/echo.schema.json", "gocontract/testdata/echo/cordis_gen.json"]:
    with open(path, encoding="utf-8") as stream:
        validator.validate(json.load(stream))
for declaration in [
    {"schema": "cordis-go.contract/v1", "definitions": [{"id": "only-config"}]},
    {"schema": "cordis-go.contract/v1", "types": {"Name": {"type": "string"}}},
    {"schema": "cordis-go.contract/v1", "types": {"Labels": {"type": "object", "additionalProperties": {"type": "string"}}}},
]:
    validator.validate(declaration)
for declaration in [
    {"schema": "cordis-go.contract/v1", "definitions": [{"id": "client", "requires": [{"name": "x", "service": "x", "maxMinor": 0}]}]},
    {"schema": "cordis-go.contract/v1", "services": [{"name": "x", "version": 1.2, "methods": []}]},
    {"schema": "cordis-go.contract/v1", "services": [{"name": "x", "major": 1, "methods": []}]},
    {"schema": "cordis-go.contract/v1", "types": {"Open": {"type": "object", "additionalProperties": True}}},
]:
    assert not validator.is_valid(declaration), declaration
print(f"Draft 2020-12: {len(vectors)} exported schemas, {count} data vectors, author declaration schema and vectors passed")
