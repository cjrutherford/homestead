# Everything Tasks image (S367, ADR-053). Build context: the repository root.
#   docker build -f images/everything/Dockerfile -t homestead/everything-tasks:<version> .
#
# Three stages, no network access outside `npm ci` (lockfile-verified
# integrity) and `go mod download` (go.sum-verified):
#   1. prepare: verify the pinned upstream archive and apply the Homestead
#      patches (scripts/everything/prepare_tasks.py).
#   2. web:     npm ci --ignore-scripts && npm run build.
#   3. bridge:  the Go bridge, production tags, CGO off, -trimpath.
# The final image is distroless/static: no shell, no package manager, uid
# 65532, only /data writable. All file times are set to 0 so identical
# inputs give identical layers. Base images are pinned by index digest;
# digests and licences are recorded in docs/THIRD_PARTY.md.
ARG PYTHON=python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8
ARG NODE=node:24-alpine@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85
ARG GOLANG=golang:1.26-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308
ARG RUNTIME=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

FROM ${PYTHON} AS prepare
COPY third_party/everything/pin.yaml third_party/everything/tasks-source.tar.gz /in/
COPY scripts/everything/prepare_tasks.py scripts/everything/puter-bridge.js scripts/everything/tasks-sw.template.js /scripts/everything/
RUN set -eu; \
    want="$(sed -n 's/^sha256: //p' /in/pin.yaml)"; \
    echo "${want}  /in/tasks-source.tar.gz" | sha256sum -c -; \
    python3 -I /scripts/everything/prepare_tasks.py /in/tasks-source.tar.gz /scripts/everything/puter-bridge.js /work

FROM ${NODE} AS web
COPY --from=prepare /work /work
WORKDIR /work/src/tasks
RUN npm ci --ignore-scripts --no-audit --no-fund && npm run build
RUN set -eu; \
    test -f dist/index.html; \
    if grep -rIl 'js\.puter\.com' dist; then echo "hosted Puter SDK reference in build" >&2; exit 1; fi; \
    mkdir -p /out/srv && cp -a dist /out/srv/app

FROM ${GOLANG} AS bridge
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/everything-bridge/ cmd/everything-bridge/
COPY internal/everything/ internal/everything/
RUN mkdir -p /out/data && \
    go build -trimpath -ldflags '-s -w -buildid=' -o /out/everything-bridge ./cmd/everything-bridge

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS assemble
COPY --from=web /out/ /out/
COPY --from=bridge /out/ /out/
RUN find /out -type d -exec chmod 755 {} + && \
    find /out -type f -exec chmod 644 {} + && \
    chmod 755 /out/everything-bridge && chmod 700 /out/data && \
    chown -R 0:0 /out && chown 65532:65532 /out/data && \
    find /out -exec touch -h -d @0 {} +

FROM ${RUNTIME}
COPY --from=assemble /out/ /
ENV HOMESTEAD_EVERYTHING_STATIC_DIR=/srv/app \
    HOMESTEAD_EVERYTHING_LISTEN=0.0.0.0:8081 \
    HOMESTEAD_EVERYTHING_APP=tasks
USER 65532:65532
VOLUME /data
EXPOSE 8081
ENTRYPOINT ["/everything-bridge"]
CMD ["-db", "/data/bridge.db"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/everything-bridge", "healthcheck"]
