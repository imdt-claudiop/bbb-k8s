# BigBlueButton 4.1 recording processing: record-and-playback core (rap-starter, resque workers)
# with the presentation format. Build context "src" = the bigbluebutton monorepo at the pinned
# commit (see images/build.sh).
FROM ubuntu:24.04 AS mkclean
# bbb-mkclean (build/packages-template/bbb-mkclean/build.sh): mkclean 0.8.10 from source.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget bzip2 build-essential \
    && cd /tmp && wget -q https://sourceforge.net/projects/matroska/files/mkclean/mkclean-0.8.10.tar.bz2/download -O mkclean.tar.bz2 \
    && tar -xjf mkclean.tar.bz2 && cd mkclean-0.8.10 \
    && make -C corec/tools/coremake && corec/tools/coremake/coremake $(corec/tools/coremake/system_output.sh) \
    && make && cp release/gcc_linux_x64/mkclean /usr/bin/mkclean

FROM ubuntu:24.04
ARG SOURCE_SHA=unknown
LABEL org.opencontainers.image.source="https://github.com/imdt-claudiop/bbb-k8s" \
      org.opencontainers.image.description="BigBlueButton 4.1 recording processing (presentation)" \
      org.bigbluebutton.source-sha="${SOURCE_SHA}"
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8 LC_ALL=C.UTF-8
# Runtime dependencies of bbb-record-core (opts-noble.sh) and bbb-playback-presentation, plus the
# build tools bundle needs for native gems; redis-server is its own pod.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg gir1.2-pango-1.0 libcurl4 libncurses6 libsystemd0 poppler-utils \
        python3 python3-attr python3-cairo python3-gi python3-gi-cairo python3-icu python3-lxml \
        rsync ruby ruby-bundler ruby-dev zlib1g build-essential libsystemd-dev zlib1g-dev \
        imagemagick ca-certificates wget \
    && wget -q -O /usr/local/bin/yq https://github.com/mikefarah/yq/releases/download/v4.44.3/yq_linux_amd64 \
    && chmod 755 /usr/local/bin/yq \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 998 bigbluebutton && useradd -m -u 998 -g 998 bigbluebutton
COPY --from=mkclean /usr/bin/mkclean /usr/bin/mkclean
# bbb-record-core: scripts, lib, Gemfile, Rakefile; bbb-playback-presentation: its scripts.
COPY --from=src record-and-playback/core/scripts /usr/local/bigbluebutton/core/scripts
COPY --from=src record-and-playback/core/lib /usr/local/bigbluebutton/core/lib
COPY --from=src record-and-playback/core/Gemfile record-and-playback/core/Gemfile.lock record-and-playback/core/Rakefile /usr/local/bigbluebutton/core/
COPY --from=src record-and-playback/presentation/scripts /usr/local/bigbluebutton/core/scripts
WORKDIR /usr/local/bigbluebutton/core
RUN bundle config set --local deployment true \
    && bundle install \
    && bundle clean \
    && rm -rf vendor/bundle/ruby/*/cache \
    && find vendor/bundle -name '*.o' -delete \
    && rm -f scripts/presentation.nginx \
    && chmod -R a+rX /usr/local/bigbluebutton
# Postinst of bbb-record-core: let ImageMagick write PDF.
RUN if [ -f /etc/ImageMagick-6/policy.xml ]; then \
      sed -i 's/<policy domain="coder" rights="none" pattern="PDF" \/>/<policy domain="coder" rights="write" pattern="PDF" \/>/g' /etc/ImageMagick-6/policy.xml; fi
# The rap logger is hard-wired to journald (core/lib/recordandplayback.rb), which a container
# does not have: log to stdout instead (upstream candidate U11).
# stdout is not a terminal in a container: sync it, or the long-running rap-starter never
# flushes its log lines.
RUN grep -q "Journald::Logger.new('bbb-rap')" lib/recordandplayback.rb \
    && sed -i "s|Journald::Logger.new('bbb-rap')|(\$stdout.sync = true; Logger.new(\$stdout))|" lib/recordandplayback.rb \
    && grep -q '(\$stdout.sync = true; Logger.new(\$stdout))' lib/recordandplayback.rb
COPY entrypoint.sh /entrypoint.sh
RUN chmod 755 /entrypoint.sh && mkdir -p /var/log/bigbluebutton /etc/bigbluebutton/recording \
    && chown -R 998:998 /var/log/bigbluebutton /etc/bigbluebutton
WORKDIR /usr/local/bigbluebutton/core/scripts
USER 998:998
ENV HOME=/tmp
ENTRYPOINT ["/entrypoint.sh"]
