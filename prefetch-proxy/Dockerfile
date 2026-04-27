# 阶段 1: 编译 Go 程序
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
# 若有 go.sum 也一并复制 (可选)
COPY go.sum ./
# 初始化并下载依赖
RUN export GOPROXY=https://goproxy.cn && go mod download 
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o prefetch-proxy .

# 阶段 2: 下载 Mihomo 二进制文件
FROM alpine:latest AS mihomo-downloader
WORKDIR /tmp
RUN apk add --no-cache curl gzip
# 获取最新稳定版 Mihomo (amd64)
RUN curl -L -o mihomo.gz https://github.com/MetaCubeX/mihomo/releases/download/v1.18.2/mihomo-linux-amd64-v1.18.2.gz \
    && gzip -d mihomo.gz \
    && chmod +x mihomo

# 阶段 3: 构建最终运行镜像
FROM alpine:latest
WORKDIR /app
# 添加时区数据和 CA 证书（HTTPS 请求必须）
RUN apk add --no-cache tzdata ca-certificates

# 拷贝组件
COPY --from=builder /app/prefetch-proxy /app/
COPY --from=mihomo-downloader /tmp/mihomo /usr/local/bin/mihomo

# 声明环境变量默认值
ENV LISTEN_ADDR=":8080"
ENV SUBCONVERTER_URL="http://subconverter:25500"
ENV TARGET_DOMAINS="special-provider.com"
ENV MIHOMO_PATH="/usr/local/bin/mihomo"
ENV PROXY_PORT="28080"
ENV INTERNAL_BASE_URL="http://prefetch-proxy:8080"
ENV DEBUG="false"

# 文件挂载环境变量预留
ENV RULE_LIST_PATH=""
ENV FAKE_IP_FILTER_PATH=""

EXPOSE 8080

CMD ["/app/prefetch-proxy"]