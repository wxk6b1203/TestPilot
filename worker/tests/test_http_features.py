"""http_exec 本轮补齐：cookies / tls_verify / comment_tolerant_json / binary_ref。"""

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker import http_exec
from testpilot_worker.engine import CaseRunner


def test_strip_json_comments_and_trailing_commas():
    import json
    src = '{\n  // line\n  "a": 1, /* block */\n  "b": [1, 2,],\n}'
    cleaned = http_exec._strip_json_comments(src)
    assert json.loads(cleaned) == {"a": 1, "b": [1, 2]}


def test_build_request_cookies_and_binary_base64():
    api = pb.HttpApi(method=pb.HTTP_METHOD_POST, uri="/upload")
    api.cookies.add(name="session", value="{{token}}")
    api.body.content_type = pb.BODY_CONTENT_TYPE_BINARY
    api.body.binary_ref = "base64:aGVsbG8="
    kwargs, snap = http_exec.build_request(api, "http://example.com", {"token": "t-1"})
    assert kwargs["cookies"] == {"session": "t-1"}
    assert kwargs["content"] == b"hello"
    assert snap["cookies"] == {"session": "t-1"}
    assert snap["body"] == "<5 bytes>"


def test_build_request_comment_tolerant_json():
    api = pb.HttpApi(method=pb.HTTP_METHOD_POST, uri="/json")
    api.body.content_type = pb.BODY_CONTENT_TYPE_JSON
    api.body.raw = '{\n // comment\n "a": 1,\n}'
    api.settings.comment_tolerant_json = True
    kwargs, _ = http_exec.build_request(api, "http://example.com", {})
    assert kwargs["content"] == '{"a": 1}'


def test_client_selection_tls_verify_default_and_explicit_false():
    task = wpb.TaskAssignment()
    r = CaseRunner(task)
    api = pb.HttpApi()
    assert r._client_for(api) is r.client
    api.settings.tls_verify = False
    assert r._client_for(api) is r.insecure_client


def test_binary_ref_missing_inline_files_fails():
    api = pb.HttpApi(method=pb.HTTP_METHOD_POST, uri="/upload")
    api.body.content_type = pb.BODY_CONTENT_TYPE_BINARY
    api.body.binary_ref = "artifact:123"
    try:
        http_exec.build_request(api, "http://example.com", {})
    except ValueError as e:
        assert "not resolved" in str(e)
    else:
        raise AssertionError("should fail without inline_files")
