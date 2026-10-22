# The kagent controller of this release with Giant Swarm's authenticator and
# authorizer (giantswarm/kagent-extensions), built against this tree: the
# image of release X.Y.Z embeds exactly the controller of release X.Y.Z, so it
# runs under the kagent chart of the same release. kagent-extensions.commit
# pins the extensions source.
ARG BASE_IMAGE_REGISTRY=cgr.dev
ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM $BASE_IMAGE_REGISTRY/chainguard/go:latest AS builder
ARG TARGETARCH
ARG VERSION
ARG LDFLAGS
ARG FORK_COMMIT

WORKDIR /workspace/kagent
COPY . .
WORKDIR /workspace/kagent-extensions
# The extensions module builds against this tree instead of the release its
# go.mod pins, with this tree's own replace lines (a dependency's replace lines
# do not apply to the main module). FORK_VERSION is the release the binary
# checks the chart's IMAGE_TAG against at start.
RUN --mount=type=cache,target=/root/go/pkg/mod,rw      \
    --mount=type=cache,target=/root/.cache/go-build,rw \
    set -eu; \
    commit=$(tr -d '[:space:]' < /workspace/kagent/kagent-extensions.commit); \
    git init -q . && \
    git fetch -q --depth 1 https://github.com/giantswarm/kagent-extensions "$commit" && \
    git checkout -q FETCH_HEAD; \
    test "$(git rev-parse HEAD)" = "$commit"; \
    if grep -q '^replace (' /workspace/kagent/go.mod; then \
        echo "ERROR: go.mod has a replace block, which this build does not copy" >&2; exit 1; \
    fi; \
    go mod edit -replace github.com/kagent-dev/kagent/go=/workspace/kagent; \
    grep '^replace ' /workspace/kagent/go.mod | while read -r _ old _ new version; do \
        go mod edit -replace "${old}=${new}@${version}"; \
    done; \
    printf '%s %.12s\n' "$VERSION" "$FORK_COMMIT" > FORK_VERSION; \
    for attempt in 1 2 3; do \
        go mod tidy && break; \
        status=$?; [ "$attempt" -lt 3 ] || exit "$status"; \
        echo "go mod tidy failed (attempt $attempt of 3), retrying in 5s" >&2; sleep 5; \
    done; \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags "$LDFLAGS" -o /app .

FROM alpine:3.24
# apk retries a transient mirror error; a missing package still fails on the third attempt.
RUN for attempt in 1 2 3; do \
        apk upgrade --no-cache && \
        apk add --no-cache ca-certificates && break; \
        status=$?; [ "$attempt" -lt 3 ] || exit "$status"; \
        echo "apk failed (attempt $attempt of 3), retrying in 5s" >&2; sleep 5; \
    done && \
    addgroup -S -g 65532 kagent && \
    adduser -S -D -u 65532 -G kagent -h /home/kagent kagent
COPY --from=builder /app /app
USER 65532:65532
ENV HOME=/home/kagent
ARG VERSION
LABEL org.opencontainers.image.source=https://github.com/giantswarm/kagent-extensions
LABEL org.opencontainers.image.description="The kagent controller with Giant Swarm's authenticator and authorizer."
LABEL org.opencontainers.image.version="$VERSION"
ENTRYPOINT ["/app"]
