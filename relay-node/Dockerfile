FROM node:22-slim

WORKDIR /app

# BaseNode
COPY shared/ ./shared/

# RelayNode
COPY relay-node/package*.json ./
RUN npm install

COPY relay-node/src/ ./src/

# API and RTP ports
EXPOSE 7070
# 100 porte ma in realtà non servono poi vediamo
EXPOSE 5000-5100/udp  

CMD ["node", "src/index.js"]