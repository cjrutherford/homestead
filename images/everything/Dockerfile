# Everything bridge image recipe (S368, ADR-057). One recipe for every
# Everything app; the app is chosen by build args. It is built on the
# appliance by the verify-then-build operation (internal/craft bridge.go)
# from this directory, shipped with the appliance beside the pinned source
# archives. It is NOT part of the owner-signed craft context (images/craft).
#
#   APP              app name (scripts/everything/apps/<app>.json here)
#   ARTIFACT_FILE    the pinned upstream source archive, staged in artifact/
#   ARTIFACT_SHA256  its digest; the build fails if the staged file differs
#
# Stages: prepare (verify archive, apply the Homestead patches), web
# (npm ci --ignore-scripts, lockfile-verified; npm run build), bridge (Go,
# production tags, CGO off, go.sum-verified), then a distroless/static
# runtime (uid 65532, only /data writable). This recipe needs the build
# network for those two lockfile-verified downloads. Base images are pinned
# by index digest; digests and licences are in docs/THIRD_PARTY.md. The
# bridge sources here are a copy of cmd/everything-bridge, internal/everything
# and scripts/everything made by scripts/everything/sync-context.sh; a test
# keeps the copy identical.
ARG PYTHON=python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8
ARG NODE=node:24-alpine@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85
ARG GOLANG=golang:1.26-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308
ARG RUNTIME=gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

FROM ${PYTHON} AS prepare
ARG APP
ARG ARTIFACT_FILE
ARG ARTIFACT_SHA256
COPY artifact/ /in/
COPY scripts/everything/ /scripts/everything/
RUN set -eu; \
    test -n "${APP}" && test -n "${ARTIFACT_FILE}" && test -n "${ARTIFACT_SHA256}"; \
    echo "${ARTIFACT_SHA256}  /in/${ARTIFACT_FILE}" | sha256sum -c -; \
    sub="$(python3 -I /scripts/everything/prepare_app.py --print-subtree "${APP}" /in/x /scripts/everything/puter-bridge.js /x)"; \
    python3 -I /scripts/everything/prepare_app.py "${APP}" "/in/${ARTIFACT_FILE}" /scripts/everything/puter-bridge.js /work; \
    test -d "/work/${sub}"; echo "${sub}" > /work/.subtree

FROM ${NODE} AS web
COPY --from=prepare /work /work
RUN cd "/work/$(cat /work/.subtree)" && npm ci --ignore-scripts --no-audit --no-fund && npm run build
RUN set -eu; \
    d="/work/$(cat /work/.subtree)/dist"; \
    test -f "${d}/index.html"; \
    if grep -rIlE 'js\.puter\.com|plausible\.io' "${d}"; then echo "hosted Puter SDK or analytics reference in build" >&2; exit 1; fi; \
    mkdir -p /out/srv && cp -a "${d}" /out/srv/app

FROM ${GOLANG} AS bridge
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
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
ARG APP
COPY --from=assemble /out/ /
ENV HOMESTEAD_EVERYTHING_STATIC_DIR=/srv/app \
    HOMESTEAD_EVERYTHING_LISTEN=0.0.0.0:8081 \
    HOMESTEAD_EVERYTHING_APP=${APP}
USER 65532:65532
VOLUME /data
EXPOSE 8081
ENTRYPOINT ["/everything-bridge"]
CMD ["-db", "/data/bridge.db"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/everything-bridge", "healthcheck"]
