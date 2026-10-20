# bbb-export-annotations: the Redis worker that renders whiteboard annotations onto the presentation
# slides and uploads the resulting PDF back to bbb-web. Built from the monorepo at the pinned commit
# (same deps as build/packages-template/bbb-export-annotations). config/settings.json is replaced by
# the chart (Service hosts, internal bbbWebAPI).
FROM node:24-bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends python3 build-essential \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=src bbb-export-annotations/ ./
RUN npm ci --omit=dev --no-progress

FROM node:24-bookworm-slim
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 export annotations worker" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
# Rendering toolchain the worker shells out to (config/settings.json points at these paths).
RUN apt-get update && apt-get install -y --no-install-recommends ghostscript cairosvg poppler-utils \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /usr/local/bigbluebutton/bbb-export-annotations
COPY --from=build /app/ ./
COPY --from=src build/packages-template/bbb-export-annotations/fonts/IBMPlexMono-Medium.woff2 \
                 build/packages-template/bbb-export-annotations/fonts/IBMPlexSans-Medium.woff2 \
                 build/packages-template/bbb-export-annotations/fonts/IBMPlexSerif-Medium.woff2 \
                 build/packages-template/bbb-export-annotations/fonts/Shantell_Sans-Tldrawish.woff2 \
                 /usr/local/share/fonts/
COPY --from=src bigbluebutton-config/slides/blank-svg.svg /usr/share/bigbluebutton/blank/blank-svg.svg
USER node
CMD ["node", "master.js"]
