# syntax=docker/dockerfile:1

# The demo shop: one image, each container runs one of its parts.
#   docker build -t signalbox-demo-shop:dev examples/demo-shop

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /demoshop .
RUN mkdir /demo-run

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /demoshop /demoshop
# Where the seed writes the ingest key for the other parts. A named volume
# mounted here starts with this directory's owner, so nonroot can write.
COPY --from=build --chown=65532:65532 /demo-run /run/demo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/demoshop"]
