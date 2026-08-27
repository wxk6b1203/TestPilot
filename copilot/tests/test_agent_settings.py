"""build_agent 的采样参数接线：显式配置 → ModelSettings；未配置 → None（按 Provider 默认）。"""

from __future__ import annotations

from testpilot_copilot.agent import build_agent
from testpilot_copilot.config import Settings


def test_model_settings_explicit():
    a = build_agent(Settings(api_key="x", temperature=0.2, top_p=0.9,
                             summarizer_temperature=0.4))
    assert a.model_settings == {"temperature": 0.2, "top_p": 0.9}


def test_model_settings_partial():
    a = build_agent(Settings(api_key="x", top_p=0.95))
    assert a.model_settings == {"top_p": 0.95}       # 只带显式键，不带 temperature


def test_model_settings_unset_means_none():
    a = build_agent(Settings(api_key="x"))
    assert a.model_settings is None                  # 请求完全不带采样字段
