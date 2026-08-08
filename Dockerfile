# One portable Dockerfile that also cross-compiles (ADR-0003, ADR-0015).
#
# The builder is pinned to the *build* machine's architecture and `go build`
# targets $TARGETARCH, so an amd64 CI runner produces the box's ARM binary
# natively — seconds, not the minutes QEMU emulation would cost. Both stages that
# run commands are pinned the same way; the final stage runs none, because a RUN
# there would execute under emulation for a foreign target.
#
# BuildKit fills TARGETOS/TARGETARCH in automatically, so a plain `docker build`
# on any machine still produces an image for that machine.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -o /bin/mdfly-server ./cmd/mdfly-server

# Architecture-independent files for the runtime image, assembled natively so the
# final stage needs no package manager.
FROM --platform=$BUILDPLATFORM alpine:3.19 AS rootfs
RUN apk add --no-cache ca-certificates tzdata

FROM alpine:3.19
COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /bin/mdfly-server /bin/mdfly-server
# Alpine's built-in unprivileged user, rather than an adduser RUN that would need
# emulating. The binary is root-owned and world-executable; nothing is written to
# disk at runtime.
USER nobody
ENTRYPOINT ["/bin/mdfly-server"]
