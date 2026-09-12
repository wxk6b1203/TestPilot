# Copilot 镜像。构建上下文 = 仓库根目录。
#   docker build -f deploy/copilot.Dockerfile -t testpilot/copilot .
FROM python:3.13-slim
ARG PIP_INDEX_URL=https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple
WORKDIR /app
# 依赖按 uv.lock 冻结安装（此前 pip install . 直接按 pyproject 解析，除
# pydantic-ai-slim 外全部漂移；uv.lock 里锁定的 vercel_ai adapter 等契约敏感
# 包随上游小版本变化会直接打进生产镜像）。uv export 含 vendor wheel 的相对
# 路径引用（./vendor/...），故 vendor 必须与 requirements 同在 WORKDIR 下。
COPY copilot/pyproject.toml copilot/uv.lock ./
COPY copilot/vendor ./vendor
RUN pip config set global.index-url "$PIP_INDEX_URL" \
    && pip config set global.extra-index-url "https://pypi.org/simple" \
    && pip install --no-cache-dir uv \
    && uv export --frozen --no-dev --no-emit-project -o requirements.txt \
    && pip install --no-cache-dir -r requirements.txt
COPY copilot/src ./src
RUN pip install --no-cache-dir --no-deps .
# 非 root 运行（无卷挂载，降权无副作用）
RUN useradd -m tp && chown -R tp:tp /app
USER tp
ENV PYTHONPATH=/app/src
EXPOSE 8100
ENTRYPOINT ["python", "-m", "testpilot_copilot.main"]
