# Applies the BigBlueButton Hasura metadata (bbb-graphql-server/metadata of the pinned commit).
# Build context "src" = the bigbluebutton monorepo at the pinned commit (see images/build.sh).
ARG HASURA_VERSION=v2.49.3

FROM ubuntu:24.04
ARG HASURA_VERSION
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 Hasura metadata apply" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL -o /usr/local/bin/hasura \
       "https://github.com/hasura/graphql-engine/releases/download/${HASURA_VERSION}/cli-hasura-linux-amd64" \
    && chmod 755 /usr/local/bin/hasura \
    && curl -fsSL -o /usr/local/bin/yq https://github.com/mikefarah/yq/releases/download/v4.44.3/yq_linux_amd64 \
    && chmod 755 /usr/local/bin/yq \
    && groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton
COPY --from=src --chown=998:998 bbb-graphql-server/metadata /app/metadata
COPY --from=src --chown=998:998 bbb-graphql-server/config.yaml /app/config.yaml
# The BigBlueButton source carries a literal URL with 127.0.0.1 and a password
# (metadata/databases/databases.yaml); read it from the environment instead.
RUN yq -i '(.[] | select(.name == "BigBlueButton") | .configuration.connection_info.database_url) = {"from_env": "HASURA_GRAPHQL_BBB_DATABASE_URL"}' \
       /app/metadata/databases/databases.yaml \
    && test "$(yq '.[] | select(.name == "BigBlueButton") | .configuration.connection_info.database_url.from_env' /app/metadata/databases/databases.yaml)" = HASURA_GRAPHQL_BBB_DATABASE_URL \
    && rm /usr/local/bin/yq
COPY apply.sh /app/apply.sh
USER 998:998
WORKDIR /app
ENV HOME=/home/bigbluebutton
ENTRYPOINT ["/app/apply.sh"]
