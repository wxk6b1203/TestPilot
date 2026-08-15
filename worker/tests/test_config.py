"""config.py：三级配置优先级 CLI > env > YAML > 默认。"""

import pytest

from testpilot_worker.config import Settings, _to_bool, apply_environ, build_parser, resolve


def _args(*argv: str):
    return build_parser().parse_args(list(argv))


def _write_yaml(tmp_path, text: str) -> str:
    p = tmp_path / "worker.yaml"
    p.write_text(text, encoding="utf-8")
    return str(p)


# ---- 默认值 ----

def test_defaults():
    s = resolve(_args(), env={})
    assert s == Settings()
    assert s.scheduler == "127.0.0.1:9090"
    assert s.max_concurrency == 4
    assert s.sandbox_net == "deny"
    assert s.egress_block_private is False


# ---- YAML 层 ----

def test_yaml_overrides_defaults(tmp_path):
    path = _write_yaml(tmp_path, "scheduler: yaml-host:1\nmax_concurrency: 9\nsandbox_net: allow\n")
    s = resolve(_args("--config", path), env={})
    assert s.scheduler == "yaml-host:1"
    assert s.max_concurrency == 9
    assert s.sandbox_net == "allow"
    assert s.name == ""  # 未配置项回落默认


def test_yaml_unknown_keys_ignored(tmp_path):
    path = _write_yaml(tmp_path, "totally_unknown: 1\nname: w1\n")
    s = resolve(_args("--config", path), env={})
    assert s.name == "w1"


def test_yaml_non_mapping_top_level_rejected(tmp_path):
    path = _write_yaml(tmp_path, "- just\n- a\n- list\n")
    with pytest.raises(SystemExit, match="顶层必须是 mapping"):
        resolve(_args("--config", path), env={})


def test_yaml_empty_file_falls_back(tmp_path):
    path = _write_yaml(tmp_path, "")
    assert resolve(_args("--config", path), env={}) == Settings()


# ---- env 覆盖 YAML ----

def test_env_overrides_yaml(tmp_path):
    path = _write_yaml(tmp_path, "scheduler: yaml-host:1\nmax_concurrency: 9\n")
    s = resolve(_args("--config", path),
                env={"TP_WORKER_SCHEDULER": "env-host:2", "TP_WORKER_MAX_CONCURRENCY": "7"})
    assert s.scheduler == "env-host:2"
    assert s.max_concurrency == 7


def test_env_empty_string_does_not_override(tmp_path):
    """env.get() 为空串时 falsy，视为未设置。"""
    path = _write_yaml(tmp_path, "scheduler: yaml-host:1\n")
    s = resolve(_args("--config", path), env={"TP_WORKER_SCHEDULER": ""})
    assert s.scheduler == "yaml-host:1"


# ---- CLI 覆盖 env ----

def test_cli_overrides_env_and_yaml(tmp_path):
    path = _write_yaml(tmp_path, "scheduler: yaml-host:1\n")
    s = resolve(_args("--config", path, "--scheduler", "cli-host:3", "--max-concurrency", "2"),
                env={"TP_WORKER_SCHEDULER": "env-host:2", "TP_WORKER_MAX_CONCURRENCY": "7"})
    assert s.scheduler == "cli-host:3"
    assert s.max_concurrency == 2


def test_cli_explicit_default_value_still_wins(tmp_path):
    """显式传了与默认值相同的值也算显式（默认值 None 区分）。"""
    path = _write_yaml(tmp_path, "max_concurrency: 9\n")
    s = resolve(_args("--config", path, "--max-concurrency", "4"), env={})
    assert s.max_concurrency == 4


# ---- 类型转换 ----

@pytest.mark.parametrize("raw", ["1", "true", "TRUE", "yes", "on"])
def test_bool_env_truthy(raw):
    s = resolve(_args(), env={"TP_EGRESS_BLOCK_PRIVATE": raw})
    assert s.egress_block_private is True


@pytest.mark.parametrize("raw", ["0", "false", "no", "off", "junk"])
def test_bool_env_falsy(raw):
    s = resolve(_args(), env={"TP_EGRESS_BLOCK_PRIVATE": raw})
    assert s.egress_block_private is False


def test_bool_cli_choices():
    assert resolve(_args("--egress-block-private", "true"), env={}).egress_block_private is True
    assert resolve(_args("--egress-block-private", "0"), env={}).egress_block_private is False


def test_bool_yaml_native():
    assert _to_bool(True) is True
    assert _to_bool(False) is False


def test_int_env_string_normalized():
    s = resolve(_args(), env={"TP_WORKER_TENANT_ID": "42"})
    assert s.tenant_id == 42
    assert isinstance(s.tenant_id, int)


def test_int_invalid_raises_system_exit():
    with pytest.raises(SystemExit, match="不是整数"):
        resolve(_args(), env={"TP_WORKER_MAX_CONCURRENCY": "abc"})


def test_string_list_fields_stay_strings():
    """capabilities/tags/egress_allow 为逗号分隔字符串，配置层不做拆分。"""
    s = resolve(_args(), env={
        "TP_WORKER_CAPABILITIES": "functional,ui",
        "TP_WORKER_TAGS": "a,b",
        "TP_EGRESS_ALLOW": "api.example.com,.corp.internal",
        "TP_SANDBOX_NET": "allow",
    })
    assert s.capabilities == "functional,ui"
    assert s.tags == "a,b"
    assert s.egress_allow == "api.example.com,.corp.internal"
    assert s.sandbox_net == "allow"


# ---- legacy 环境变量键 ----

def test_legacy_env_keys(tmp_path):
    s = resolve(_args(), env={
        "TP_ARTIFACT_DIR": "/tmp/arts",
        "TP_EGRESS_ALLOW": "api.example.com",
        "TP_SANDBOX_CPU": "50",
        "TP_SANDBOX_MEM_MB": "2048",
        "TP_SANDBOX_NPROC": "256",
        "TP_SANDBOX_NOFILE": "512",
        "TP_SANDBOX_FSIZE_MB": "64",
        "TP_SANDBOX_NET": "allow",
        "TP_OTEL_EXPORTER": "stdout",
        "TP_OTEL_ENDPOINT": "collector:4317",
    })
    assert s.artifact_dir == "/tmp/arts"
    assert s.egress_allow == "api.example.com"
    assert s.sandbox_cpu == 50
    assert s.sandbox_mem_mb == 2048
    assert s.sandbox_nproc == 256
    assert s.sandbox_nofile == 512
    assert s.sandbox_fsize_mb == 64
    assert s.sandbox_net == "allow"
    assert s.otel_exporter == "stdout"
    assert s.otel_endpoint == "collector:4317"


def test_unknown_env_key_ignored():
    s = resolve(_args(), env={"TP_WORKER_NOPE": "x", "TP_WHATEVER": "y"})
    assert s == Settings()


# ---- apply_environ 回写 ----

@pytest.fixture(autouse=True)
def _clean_tp_env():
    """apply_environ 直接写 os.environ（monkeypatch 的 delenv 在 teardown 会恢复
    删除前的值，等于把污染带回来）；这里在测试后强制清除全部 TP_* 残留，
    避免影响后续测试（egress/sandbox 按 env 惰性读取，残留会改变行为）。"""
    yield
    import os
    for key in list(os.environ):
        if key.startswith("TP_"):
            os.environ.pop(key, None)


def test_apply_environ_writes_back():
    apply_environ(Settings(artifact_dir="/x", egress_block_private=True, sandbox_cpu=55))
    import os
    assert os.environ["TP_ARTIFACT_DIR"] == "/x"
    assert os.environ["TP_EGRESS_BLOCK_PRIVATE"] == "1"
    assert os.environ["TP_SANDBOX_CPU"] == "55"


def test_apply_environ_bool_false_writes_zero():
    apply_environ(Settings(egress_block_private=False))
    import os
    assert os.environ["TP_EGRESS_BLOCK_PRIVATE"] == "0"
