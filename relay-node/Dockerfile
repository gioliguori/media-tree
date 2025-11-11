FROM node:22-slim

WORKDIR /app

# BUILD TOOLS + GSTREAMER
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    make \
    pkg-config \
    # GStreamer runtime
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    # GStreamer development
    libgstreamer1.0-dev \
    libgstreamer-plugins-base1.0-dev \
    # Pulisci cache
    && rm -rf /var/lib/apt/lists/*

# COPY & BUILD C
COPY relay-node/forwarder/ ./forwarder/

WORKDIR /app/forwarder
RUN make
RUN ls -lh relay-forwarder

# Torna alla working directory principale
WORKDIR /app

COPY shared/ ./shared/

COPY relay-node/package*.json ./
RUN npm install

COPY relay-node/src/ ./src/

# API port
EXPOSE 7071
# RTP ports
EXPOSE 5002/udp 5004/udp

CMD ["node", "src/index.js"]