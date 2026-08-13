# Copilot 镜像。构建上下文 = 仓库根目录。
#   docker build -f deploy/copilot.Dockerfile -t testpilot/copilot .
FROM python:3.13-slim
ARG PIP_INDEX_URL=https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple
WORKDIR /app
COPY copilot/pyproject.toml ./
COPY copilot/src ./src
# pydantic-ai-extensions 未发布到公共 PyPI，随库 vendor（copilot/vendor/）
COPY copilot/vendor ./vendor
RUN pip config set global.index-url "$PIP_INDEX_URL" \
    && pip config set global.extra-index-url "https://pypi.org/simple" \
    && pip install --no-cache-dir ./vendor/pydantic_ai_extensions-0.1.0-py3-none-any.whl \
    && pip install --no-cache-dir .
ENV PYTHONPATH=/app/src
EXPOSE 8100
ENTRYPOINT ["python", "-m", "testpilot_copilot.main"]
