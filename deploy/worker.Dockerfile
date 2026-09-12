# Worker 镜像（含 Playwright + Chromium）。构建上下文 = 仓库根目录。
#   docker build -f deploy/worker.Dockerfile -t testpilot/worker .
FROM python:3.13-slim
# pip 镜像（与宿主 uv 一致）：--build-arg PIP_INDEX_URL=https://pypi.org/simple 可覆盖
ARG PIP_INDEX_URL=https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple
WORKDIR /app
COPY worker/pyproject.toml ./
COPY worker/src ./src
# [playwright] extra 拉浏览器驱动；--with-deps 装系统依赖（需 root，slim 内为 root）
RUN pip config set global.index-url "$PIP_INDEX_URL" \
    && pip config set global.extra-index-url "https://pypi.org/simple" \
    && pip install --no-cache-dir ".[playwright]" \
    && playwright install --with-deps chromium
ENV PYTHONPATH=/app/src \
    TP_ARTIFACT_DIR=/data/artifacts
VOLUME /data
# 参数由 compose command 提供（--scheduler / --capabilities / ...）
# 已知取舍：保持 root 运行——非 root 下 Chromium 沙箱依赖 user namespaces
# （docker 默认 seccomp 常不可用），Playwright 启动需 --no-sandbox，改动涉及
# 沙箱安全语义与 compose 联动，需单独验证后再降权。低代码脚本沙箱的
# fail-closed 开关见 TP_SANDBOX_REQUIRE_ISOLATION。
ENTRYPOINT ["python", "-m", "testpilot_worker"]
