# 构建阶段：使用纯净 Go 语言依赖（modernc sqlite 无 CGO），可安全用 alpine
FROM golang:1.26 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/out/filededup .

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /app/out/filededup /app/filededup
# 数据目录（数据库）与回收站目录（可映射宿主机，便于恢复）
VOLUME ["/data", "/trash"]
ENV DATA_DIR=/data \
    LISTEN_ADDR=:8080 \
    MYFILE_ROOT=/myfile \
    TRASH_DIR=/trash
EXPOSE 8080
ENTRYPOINT ["/app/filededup"]