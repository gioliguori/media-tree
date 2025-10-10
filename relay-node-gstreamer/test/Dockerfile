FROM debian:12-slim
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    gstreamer1.0-plugins-ugly \
    gstreamer1.0-nice \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY entrypoint.sh /app/
RUN chmod +x /app/entrypoint.sh

ENV GST_DEBUG=2

ENTRYPOINT ["/app/entrypoint.sh"]