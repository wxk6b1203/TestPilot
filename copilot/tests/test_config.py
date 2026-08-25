"""config 优先级链单测：CLI > env > .env > YAML > 内置默认。

隔离策略：
- 字段合成一律走 load(argv, env=..., dotenv=...) 显式注入，不读进程 os.environ
  （模块 import 时会按设计回填真实 copilot/.env，注入 env 使其不可见）。
- YAML 路径解析 (_config_path) 会读 os.environ["TP_COPILOT_CONFIG"] 与
  ./copilot.yaml，autouse fixture 统一删除该环境变量并 chdir 到 tmp 目录。
- _parse_dotenv / _backfill_environ 通过 monkeypatch 替换 _ENV_FILE 与环境变量测试。
"""

from __future__ import annotations

import pytest

from testpilot_copilot import config
from testpilot_copilot.config import Settings, build_parser, load, resolve


@pytest.fixture(autouse=True)
def _isolated(tmp_path, monkeypatch):
    monkeypatch.delenv("TP_COPILOT_CONFIG", raising=False)
    monkeypatch.chdir(tmp_path)


def _resolve(argv: list[str] | None = None, env: dict | None = None,
             dotenv: dict | None = None) -> Settings:
    args = build_parser().parse_args(argv or [])
    return resolve(args, {} if env is None else env, {} if dotenv is None else dotenv)


def _write_yaml(tmp_path, text: str) -> str:
    p = tmp_path / "cfg.yaml"
    p.write_text(text, encoding="utf-8")
    return str(p)


# ---------------------------------------------------------------------------
# 默认值与优先级链
# ---------------------------------------------------------------------------

def test_defaults_equal_settings_dataclass():
    s = _resolve()
    assert s == Settings()
    assert s.provider == "deepseek"
    assert s.model == "deepseek-v4-flash"
    assert s.context_window == 64000
    assert s.http_timeout == 15.0
    assert isinstance(s.http_timeout, float)
    assert s.model_timeout == 120.0
    assert s.stream_idle_timeout == 300.0
    assert s.api_key == ""  # 默认无 key
    assert s.system_prompt_file == ""  # 空 = 包内置 prompt 模板
    assert s.summarizer_prompt_file == ""


def test_yaml_applies(tmp_path):
    cfg = _write_yaml(tmp_path, "model: yaml-model\ncontext_window: 4096\nhttp_timeout: 3\n")
    s = _resolve(["--config", cfg])
    assert s.model == "yaml-model"
    assert s.context_window == 4096
    assert s.http_timeout == 3.0  # YAML int 经 float 转换
    assert s.provider == "deepseek"  # 未提及字段回退默认


def test_dotenv_overrides_yaml(tmp_path):
    cfg = _write_yaml(tmp_path, "model: yaml-model\nprovider: openai_compatible\n")
    s = _resolve(["--config", cfg], dotenv={"TP_COPILOT_MODEL": "dotenv-model"})
    assert s.model == "dotenv-model"      # .env 覆盖 YAML
    assert s.provider == "openai_compatible"  # 未被覆盖的 YAML 键仍生效


def test_env_overrides_dotenv(tmp_path):
    cfg = _write_yaml(tmp_path, "model: yaml-model\n")
    s = _resolve(["--config", cfg],
                 env={"TP_COPILOT_MODEL": "env-model"},
                 dotenv={"TP_COPILOT_MODEL": "dotenv-model"})
    assert s.model == "env-model"


def test_cli_overrides_env():
    s = _resolve(["--model", "cli-model", "--context-window", "1024"],
                 env={"TP_COPILOT_MODEL": "env-model",
                      "TP_COPILOT_CONTEXT_WINDOW": "8192"})
    assert s.model == "cli-model"
    assert s.context_window == 1024


def test_prompt_file_yaml_env_and_cli(tmp_path):
    cfg = _write_yaml(tmp_path,
                      "system_prompt_file: yaml-system.md\nsummarizer_prompt_file: yaml-summarizer.md\n")
    assert _resolve(["--config", cfg]).system_prompt_file == "yaml-system.md"
    assert _resolve(["--config", cfg]).summarizer_prompt_file == "yaml-summarizer.md"
    s = _resolve(["--config", cfg], env={"TP_COPILOT_SYSTEM_PROMPT_FILE": "env-system.md"})
    assert s.system_prompt_file == "env-system.md"
    assert s.summarizer_prompt_file == "yaml-summarizer.md"
    s = _resolve(["--system-prompt-file", "cli-system.md"],
                 env={"TP_COPILOT_SYSTEM_PROMPT_FILE": "env-system.md"})
    assert s.system_prompt_file == "cli-system.md"


def test_empty_env_value_does_not_override(tmp_path):
    """现状刻画：env/.env 用真值判断，空字符串不会覆盖下层配置。"""
    cfg = _write_yaml(tmp_path, "model: yaml-model\n")
    s = _resolve(["--config", cfg], env={"TP_COPILOT_MODEL": ""})
    assert s.model == "yaml-model"


# ---------------------------------------------------------------------------
# YAML 路径解析（--config > TP_COPILOT_CONFIG > ./copilot.yaml）
# ---------------------------------------------------------------------------

def test_yaml_path_via_env_var(tmp_path, monkeypatch):
    cfg = _write_yaml(tmp_path, "model: envpath-model\n")
    monkeypatch.setenv("TP_COPILOT_CONFIG", cfg)
    s = _resolve()
    assert s.model == "envpath-model"


def test_cwd_copilot_yaml_autodiscovery(tmp_path):
    (tmp_path / "copilot.yaml").write_text("model: cwd-model\n", encoding="utf-8")
    s = _resolve()
    assert s.model == "cwd-model"


def test_yaml_top_level_not_mapping_exits(tmp_path):
    cfg = _write_yaml(tmp_path, "- just\n- a\n- list\n")
    with pytest.raises(SystemExit):
        _resolve(["--config", cfg])


# ---------------------------------------------------------------------------
# api_key：无 CLI 入口
# ---------------------------------------------------------------------------

def test_api_key_has_no_cli_flag():
    with pytest.raises(SystemExit):  # argparse: unrecognized arguments
        build_parser().parse_args(["--api-key", "secret"])
    assert "api_key" not in vars(build_parser().parse_args([]))


def test_api_key_from_env_and_yaml(tmp_path):
    cfg = _write_yaml(tmp_path, "api_key: yaml-key\n")
    assert _resolve(["--config", cfg]).api_key == "yaml-key"
    assert _resolve(env={"TP_COPILOT_API_KEY": "env-key"}).api_key == "env-key"
    assert _resolve(dotenv={"TP_COPILOT_API_KEY": "dotenv-key"}).api_key == "dotenv-key"


def test_validate_requires_api_key():
    with pytest.raises(RuntimeError, match="TP_COPILOT_API_KEY"):
        Settings().validate()
    Settings(api_key="k").validate()  # 不抛


# ---------------------------------------------------------------------------
# 类型转换
# ---------------------------------------------------------------------------

def test_http_timeout_float_conversion_from_env():
    s = _resolve(env={"TP_COPILOT_HTTP_TIMEOUT": "2.5"})
    assert s.http_timeout == 2.5
    assert isinstance(s.http_timeout, float)


def test_http_timeout_invalid_exits():
    with pytest.raises(SystemExit):
        _resolve(env={"TP_COPILOT_HTTP_TIMEOUT": "abc"})


def test_model_timeout_and_stream_idle_timeout_from_env():
    s = _resolve(env={"TP_COPILOT_MODEL_TIMEOUT": "90",
                      "TP_COPILOT_STREAM_IDLE_TIMEOUT": "0"})
    assert s.model_timeout == 90.0
    assert isinstance(s.model_timeout, float)
    assert s.stream_idle_timeout == 0.0  # 0=禁用 SSE 空闲兜底
    with pytest.raises(SystemExit):
        _resolve(env={"TP_COPILOT_MODEL_TIMEOUT": "abc"})


def test_context_window_invalid_env_exits():
    with pytest.raises(SystemExit):
        _resolve(env={"TP_COPILOT_CONTEXT_WINDOW": "notanint"})


# ---------------------------------------------------------------------------
# .env 解析与回填（纯逻辑，_ENV_FILE 指向 tmp）
# ---------------------------------------------------------------------------

def test_parse_dotenv(tmp_path, monkeypatch):
    env_file = tmp_path / ".env"
    env_file.write_text(
        '# comment\n'
        '\n'
        'TP_COPILOT_MODEL = "quoted-model"\n'
        "TP_COPILOT_API_KEY='single'\n"
        'NO_EQUALS_LINE\n'
        'TP_COPILOT_ADDR=0.0.0.0:9999\n',
        encoding="utf-8")
    monkeypatch.setattr(config, "_ENV_FILE", env_file)
    d = config._parse_dotenv()
    assert d == {"TP_COPILOT_MODEL": "quoted-model",
                 "TP_COPILOT_API_KEY": "single",
                 "TP_COPILOT_ADDR": "0.0.0.0:9999"}


def test_parse_dotenv_missing_file(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "_ENV_FILE", tmp_path / "nope.env")
    assert config._parse_dotenv() == {}


def test_backfill_environ_only_sets_missing(monkeypatch):
    monkeypatch.setenv("TP_COPILOT_MODEL", "real-env")
    monkeypatch.delenv("TP_COPILOT_ADDR", raising=False)
    config._backfill_environ({"TP_COPILOT_MODEL": "from-dotenv",
                              "TP_COPILOT_ADDR": "1.2.3.4:1"})
    import os
    assert os.environ["TP_COPILOT_MODEL"] == "real-env"      # 已有值不覆盖
    assert os.environ["TP_COPILOT_ADDR"] == "1.2.3.4:1"      # 缺失才回填


# ---------------------------------------------------------------------------
# load() 端到端（注入 env/dotenv）
# ---------------------------------------------------------------------------

def test_load_end_to_end(tmp_path):
    cfg = _write_yaml(tmp_path, "model: yaml-model\nhttp_timeout: 9\n")
    s = load(["--config", cfg, "--http-timeout", "1.5"],
             env={"TP_COPILOT_API_KEY": "env-key"},
             dotenv={"TP_COPILOT_API_KEY": "dotenv-key"})
    assert s.model == "yaml-model"     # YAML
    assert s.http_timeout == 1.5       # CLI 覆盖 YAML
    assert s.api_key == "env-key"      # env 覆盖 .env
    s.validate()                       # 有 key，通过
