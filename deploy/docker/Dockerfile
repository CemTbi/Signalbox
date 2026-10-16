# syntax=docker/dockerfile:1

# Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t signalbox-server:dev .
#
# The build stages run on the build machine's platform and cross-compile, so
# multi-platform images build without emulation.

# The web app, built into internal/webui/dist for go:embed. Its output is the
# same on every platform.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
COPY web/app/package.json app/
COPY web/packages/api-client/package.json packages/api-client/
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts
COPY web/ ./
RUN mkdir -p /src/internal/webui/dist && npm run build

# The server, with the web app embedded.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
ARG COMMIT=""
ARG DATE=""
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w \
      -X github.com/CemTbi/Signalbox/internal/buildinfo.version=${VERSION} \
      -X github.com/CemTbi/Signalbox/internal/buildinfo.commit=${COMMIT} \
      -X github.com/CemTbi/Signalbox/internal/buildinfo.date=${DATE}" \
    -o /out/signalbox-server ./cmd/signalbox-server

# A distroless, non-root runtime: no shell, no package manager.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=server /out/signalbox-server /signalbox-server
USER nonroot:nonroot
EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=4s --start-period=10s --retries=3 CMD ["/signalbox-server", "healthcheck"]
ENTRYPOINT ["/signalbox-server"]
CMD ["serve"]
