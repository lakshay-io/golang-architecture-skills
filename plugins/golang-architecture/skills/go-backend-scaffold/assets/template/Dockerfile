# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
# Use the image's Go; never download a toolchain mid-build.
ENV GOTOOLCHAIN=local CGO_ENABLED=0

# Dependencies first: this layer stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless/static: no shell, no package manager, CA certs + tzdata only,
# non-root user (65532). Smallest attack surface that still runs a Go binary.
FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
ENV APP_VERSION=${VERSION}
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
