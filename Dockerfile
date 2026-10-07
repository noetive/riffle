# Riffle in a container: the image a hosted agent runtime runs, speaking MCP over
# stdio with a headless Chromium beside it.
#
# The build stage is pinned to the build platform and cross-compiles to the
# target, so a linux/arm64 image builds at full speed on an amd64 runner. The
# runtime stage installs Debian's own chromium package, which exists for both
# amd64 and arm64 and is patched by Debian's security team; rebuild the image to
# pick up browser fixes. The base images are tags, not digests: pin them by
# digest in the build system that rebuilds this image on a schedule.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

ARG VERSION=docker
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /riffle ./cmd/riffle

FROM debian:bookworm-slim

# fonts-liberation and fonts-noto-color-emoji keep text and emoji from rendering
# as empty boxes in page views. ca-certificates is needed for HTTPS pages.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        chromium \
        ca-certificates \
        fonts-liberation \
        fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 --shell /usr/sbin/nologin riffle

COPY --from=build /riffle /usr/local/bin/riffle

ENV RIFFLE_CHROME=/usr/bin/chromium \
    HOME=/home/riffle

USER riffle:riffle
WORKDIR /home/riffle

# Running it. Chromium sandboxes its renderers with Linux user namespaces, and
# Docker's default seccomp profile blocks the calls it needs, so on a stock
# `docker run` the browser exits at start. Riffle never passes --no-sandbox, and
# this image does not either: the sandbox stays on. Pick one of these when you
# run the container:
#
#   docker run -i --rm --security-opt seccomp=chromium-seccomp.json IMAGE mcp
#       A seccomp profile that is Docker's default plus the namespace calls
#       (clone, unshare, setns with the CLONE_NEW* flags). Narrowest grant, and
#       the one to prefer; you supply the profile file.
#
#   docker run -i --rm --cap-add SYS_ADMIN IMAGE mcp
#       Simpler, but SYS_ADMIN is a broad capability. Use it only where the
#       container is already isolated, such as a single-purpose microVM.
#
# Check a setup with: docker run --rm [options above] IMAGE doctor
ENTRYPOINT ["riffle"]
CMD ["mcp"]
