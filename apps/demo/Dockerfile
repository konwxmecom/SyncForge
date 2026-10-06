FROM node:20-alpine
WORKDIR /workspace

COPY package*.json ./
COPY server/go.mod ./server/go.mod
COPY server/go.sum ./server/go.sum
COPY packages ./packages
COPY apps ./apps
COPY scripts ./scripts
COPY tsconfig.json ./tsconfig.json

RUN npm install

EXPOSE 5173
CMD ["npm", "run", "dev:demo", "--", "--host", "0.0.0.0"]
