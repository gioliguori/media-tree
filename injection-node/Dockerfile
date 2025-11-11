FROM node:22

WORKDIR /app

COPY injection-node/package*.json ./

# Install dependencies
RUN npm install

COPY shared/ ./shared/

# source code
COPY injection-node/src/ ./src/

EXPOSE 7070

CMD ["npm", "start"]