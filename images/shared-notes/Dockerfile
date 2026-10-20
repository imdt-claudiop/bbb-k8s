# bbb-shared-notes-server: the Hocuspocus + BlockNote collaborative notes backend, built from the
# monorepo at the pinned commit (same steps as build/packages-template/bbb-shared-notes-server).
# config/default.yml is replaced at runtime by the chart (Service hosts + the DB password from the
# bbb-db secret), so the one baked here is only a standalone-run default.
FROM node:24-bookworm-slim AS build
WORKDIR /src
# curl is needed by the package's postinstall (download-twemoji-assets.mjs); the slim base omits it.
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=src bbb-shared-notes-server/ ./
RUN npm ci --no-progress \
    && npm run build \
    && mv dist/index.js dist/bbb-shared-notes-server.js

FROM node:24-bookworm-slim
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 shared notes server" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
# wkhtmltopdf renders the PDF export (a dependency of the package, opts-noble.sh).
RUN apt-get update && apt-get install -y --no-install-recommends wkhtmltopdf \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /usr/share/bbb-shared-notes-server
COPY --from=build /src/dist/ ./
COPY --from=build /src/node_modules ./node_modules
COPY --from=build /src/assets ./assets
COPY --from=build /src/blocknote_schema.sql ./
COPY --from=build /src/config/default.yml ./config/default.yml
USER node
CMD ["node", "bbb-shared-notes-server.js"]
