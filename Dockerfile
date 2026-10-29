FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
ARG TARGETARCH
ARG ZIG=0.16.0
ARG SPIDERMONKEY=v152.0.4-aistudio2api.1
RUN apt-get update && apt-get install -y --no-install-recommends xz-utils && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://ziglang.org/download/$ZIG/zig-$(uname -m)-linux-$ZIG.tar.xz | tar -xJ -C /opt \
    && mv /opt/zig-* /opt/zig \
    && mkdir /opt/spidermonkey \
    && curl -fsSL https://github.com/Mag1cFall/firefox/releases/download/$SPIDERMONKEY/spidermonkey-$SPIDERMONKEY-linux-$TARGETARCH.tar.gz | tar -xz -C /opt/spidermonkey
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN target=$([ "$TARGETARCH" = arm64 ] && echo aarch64 || echo x86_64) \
    && CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH \
    CC="/opt/zig/zig cc -target $target-linux-musl" CXX="/opt/zig/zig c++ -target $target-linux-musl" \
    CGO_CXXFLAGS=-I/opt/spidermonkey/include CGO_LDFLAGS=-L/opt/spidermonkey/lib \
    go build -trimpath -tags netgo,osusergo -ldflags="-s -w -linkmode external -extldflags '-static -Wl,--strip-all'" -o /out/aistudio2api ./cmd/aistudio2api

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata \
    libgtk-3-0 libasound2 libnss3 libdbus-glib-1-2 libxtst6 libxrandr2 libgbm1 libxkbcommon0 \
    libpango-1.0-0 libcairo2 libxcomposite1 libxdamage1 libxfixes3 libx11-xcb1 \
    xvfb \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/aistudio2api /usr/local/bin/aistudio2api
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:2048
ENV PREVIEW_LISTEN_ADDR=0.0.0.0:2050
EXPOSE 2048 2050
CMD ["aistudio2api", "-open-ui=false"]
