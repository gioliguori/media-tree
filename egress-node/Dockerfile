FROM node:22

WORKDIR /app

# Copia package files dall'egress-node
COPY egress-node/package*.json ./

# GStreamer
RUN apt-get update && apt-get install -y --no-install-recommends \
    gstreamer1.0-tools \
    gstreamer1.0-plugins-base \
    gstreamer1.0-plugins-good \
    gstreamer1.0-plugins-bad \
    gstreamer1.0-nice \
    && rm -rf /var/lib/apt/lists/*

# Install dependencies
RUN npm install

# Copia shared BaseNode dalla root
COPY shared/ ./shared/

# Copia source code egress-node
COPY egress-node/src/ ./src/

# Copia html per testare
COPY egress-node/web/ ./web/  

# Esponi porta API
EXPOSE 7071

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD node -e "require('http').get('http://localhost:7071/status', (r) => { process.exit(r.statusCode === 200 ? 0 : 1); }).on('error', () => process.exit(1));"

CMD ["npm", "start"]