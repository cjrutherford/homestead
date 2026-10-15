# Shared static image for the craft apps (S279, ADR-028).
#
# Built on the appliance by the verify-then-build operation, never
# published. The signed craft manifest carries the digest of this
# directory (see docs/architecture/RELEASE_VERIFICATION.md), so editing a
# file here without re-signing makes the build refuse.
#
# The base is pinned by digest (the nginx 1.27-alpine multi-arch index);
# the digest and licence are recorded in docs/THIRD_PARTY.md. Updating it
# changes this directory's digest and needs a new signed manifest.
ARG BASE=nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10

FROM ${BASE} AS extract
# The daemon passes the verified artifact name and digest. The build
# fails here if the staged file does not match, independent of the
# daemon's own check.
ARG ARTIFACT_FILE
ARG ARTIFACT_SHA256
COPY artifact/ /in/
RUN set -eu; \
    test -n "${ARTIFACT_FILE}" && test -n "${ARTIFACT_SHA256}"; \
    f="/in/${ARTIFACT_FILE}"; \
    echo "${ARTIFACT_SHA256}  ${f}" | sha256sum -c -; \
    mkdir -p /site; \
    case "${ARTIFACT_FILE}" in \
      *.tar.gz|*.tgz) tar -xzf "${f}" -C /site --no-same-owner --no-same-permissions ;; \
      *.zip) unzip -q "${f}" -d /site ;; \
      *) echo "unsupported artifact type: ${ARTIFACT_FILE}" >&2; exit 1 ;; \
    esac; \
    test -f /site/index.html

FROM ${BASE}
RUN rm -rf /usr/share/nginx/html/* /etc/nginx/conf.d/default.conf
COPY --from=extract /site/ /usr/share/nginx/html/
COPY nginx.conf /etc/nginx/nginx.conf
EXPOSE 80
# Everything nginx writes at runtime lives under /tmp (pid file and
# temporary paths are set in nginx.conf), so /tmp is the only path a
# package needs as an ephemeral tmpfs.
CMD ["nginx", "-g", "daemon off;"]
