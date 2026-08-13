# Scheduler 镜像（含前端构建产物）。构建上下文 = 仓库根目录。
#   docker build -f deploy/scheduler.Dockerfile -t testpilot/scheduler .

# ---- 前端 ----
FROM node:24-alpine AS web
# 与宿主一致的镜像源（可在 build 时覆盖：--build-arg NPM_REGISTRY=https://registry.npmjs.org）
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /build
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && npm config set registry "$NPM_REGISTRY" \
    && pnpm config set registry "$NPM_REGISTRY" && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ---- Go ----
FROM golang:1.25-alpine AS sched
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY scheduler/go.mod scheduler/go.sum ./
RUN go mod download
COPY scheduler/ ./
RUN CGO_ENABLED=0 go build -o /out/scheduler ./cmd/scheduler

# ---- 运行时 ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=sched /out/scheduler /usr/local/bin/scheduler
COPY --from=web /build/dist /app/web/dist
# 容器内监听全部接口（由 compose/端口映射控制暴露面）；本机 dev 默认回环见 scripts/dev.sh
ENV TP_HTTP_ADDR=:8080 \
    TP_GRPC_ADDR=:9090 \
    TP_STATIC_DIR=/app/web/dist \
    TP_ARTIFACT_DIR=/data/artifacts \
    TP_LOG_FORMAT=json
VOLUME /data
EXPOSE 8080 9090
ENTRYPOINT ["scheduler"]
