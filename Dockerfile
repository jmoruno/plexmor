# syntax=docker/dockerfile:1
# Static Go binary on distroless: no shell, no package manager, runs as a non-root user.
# The build runs on the builder's own platform and cross-compiles, so arm64 images are quick.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# The image pins GOTOOLCHAIN=local; auto lets go.mod's toolchain line bring in the patched release.
ENV GOTOOLCHAIN=auto
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/plexmor ./cmd/plexmor

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/plexmor /plexmor
ENV DATA_DIR=/data LISTEN=:7254
VOLUME /data
EXPOSE 7254
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/plexmor", "health"]
ENTRYPOINT ["/plexmor"]
