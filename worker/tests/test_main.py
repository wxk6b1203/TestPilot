"""main.py：敏感环境变量清理正则（宁多勿漏：多删安全，漏删即凭据泄漏）。"""

from testpilot_worker.main import _SENSITIVE_ENV_RE


def test_matches_common_credential_env_names():
    """常见凭据类变量名必须命中（含新增的 CREDENTIAL/AUTH/PASSPHRASE/_PAT）。"""
    for name in [
        "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "MY_PASSWORD", "MYSQL_PASSWD",
        "DB_PASSPHRASE", "GOOGLE_APPLICATION_CREDENTIALS", "BASIC_AUTH",
        "AUTH_TOKEN", "GITHUB_PAT", "AZURE_DEVOPS_PAT", "API_SECRET_KEY",
    ]:
        assert _SENSITIVE_ENV_RE.search(name), f"missed sensitive env: {name}"


def test_does_not_match_benign_vars_needed_by_worker():
    """PATH 等良性变量必须保留：沙箱隔离工具探测（shutil.which）依赖 PATH；
    注意 _PAT 带下划线前缀，不得误伤 PATH/PYTHONPATH。"""
    for name in ["PATH", "PYTHONPATH", "HOME", "LANG", "TP_ARTIFACT_DIR",
                 "TP_SANDBOX_REQUIRE_ISOLATION", "OTEL_EXPORTER_OTLP_ENDPOINT"]:
        assert not _SENSITIVE_ENV_RE.search(name), f"over-matched benign env: {name}"
