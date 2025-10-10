FROM node:22

WORKDIR /app

# Copia package files dall'injection-node
COPY injection-node/package*.json ./

# Install dependencies
RUN npm install

# Copia shared BaseNode dalla root
COPY shared/ ./shared/

# Copia source code injection-node
COPY injection-node/src/ ./src/

# Esponi porta API
EXPOSE 7070

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD node -e "require('http').get('http://localhost:7070/status', (r) => { process.exit(r.statusCode === 200 ? 0 : 1); }).on('error', () => process.exit(1));"

CMD ["npm", "start"]