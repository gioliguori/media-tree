FROM node:22-slim

WORKDIR /app

# GStreamer
RUN apt-get update && apt-get install -y --no-install-recommends \
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    gstreamer1.0-nice \
    && rm -rf /var/lib/apt/lists/*

# BaseNode    
COPY shared/ ./shared/

# RelayNode
COPY relay-node-gstreamer/package*.json ./
RUN npm install

COPY relay-node-gstreamer/src/ ./src/

EXPOSE 7070
# 100 porte ma in realtà non servono poi vediamo
EXPOSE 5000-5100/udp

CMD ["node", "src/index.js"]