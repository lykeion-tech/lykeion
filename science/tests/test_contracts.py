"""Cross-language validation of the frozen transport, without executing science."""
import json
from pathlib import Path
import pytest
from jsonschema import Draft202012Validator
from openapi_spec_validator import validate

ROOT=Path(__file__).resolve().parents[2]
def test_openapi_document():
    validate(json.loads((ROOT/'contracts/control.openapi.json').read_text()))

def test_websocket_rejects_authority_messages():
    schema=json.loads((ROOT/'contracts/realtime.protocol.json').read_text())
    validator=Draft202012Validator(schema)
    good={'schema_version':'1','type':'document.sync','message_id':'msg_a','payload':{'document_id':'doc_a','known_revision':'0'}}
    validator.validate(good)
    assert not validator.is_valid({**good,'type':'gate.advance'})
    assert not validator.is_valid({**good,'payload':{**good['payload'],'force':True}})
