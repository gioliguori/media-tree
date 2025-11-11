FROM node:22-slim

WORKDIR /app

# BUILD TOOLS + GSTREAMER
RUN apt-get update && apt-get install -y --no-install-recommends \
    # Build tools per compilare C
    gcc \
    g++ \
    make \
    pkg-config \
    python3 \
    # GStreamer runtime
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    # GStreamer development
    libgstreamer1.0-dev \
    libgstreamer-plugins-base1.0-dev \
    # Pulisci cache
    && rm -rf /var/lib/apt/lists/*

# COPY & BUILD C
COPY egress-node/forwarder/ ./forwarder/

WORKDIR /app/forwarder
RUN make
RUN ls -lh egress-forwarder
RUN chmod +x egress-forwarder

# Torna alla working directory principale
WORKDIR /app

COPY shared/ ./shared/

COPY egress-node/package*.json ./
RUN npm install

COPY egress-node/src/ ./src/

COPY egress-node/web/ ./web/

# API port
EXPOSE 7073
# RTP input ports
EXPOSE 5002/udp 5004/udp

# START
CMD ["node", "src/index.js"]