# bbb-graphql-actions for BigBlueButton 4.1.
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
ARG BUILDER_IMAGE=bigbluebutton/bbb-build:v4.1.x-release--2026-10-06-224206

FROM ${BUILDER_IMAGE} AS builder
COPY --from=src bbb-graphql-actions /src/bbb-graphql-actions
# Same steps as build/packages-template/bbb-graphql-actions/build.sh.
RUN cd /src/bbb-graphql-actions \
    && npm ci --no-progress \
    && npm run build \
    && mkdir -p /out \
    && cp -r dist/. /out/ \
    && mv /out/index.js /out/bbb-graphql-actions.js \
    && cp package.json package-lock.json /out/ \
    && cd /out && npm ci --no-progress --omit=dev

FROM node:24-bookworm-slim
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 graphql-actions" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
RUN groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton
COPY --from=builder --chown=998:998 /out /usr/local/bigbluebutton/bbb-graphql-actions
WORKDIR /usr/local/bigbluebutton/bbb-graphql-actions
ENV NODE_ENV=production
USER 998:998
ENTRYPOINT ["node", "bbb-graphql-actions.js"]
