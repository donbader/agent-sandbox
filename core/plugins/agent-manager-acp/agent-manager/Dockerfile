FROM node:22-slim

WORKDIR /app
COPY package.json package-lock.json* tsconfig.json ./
RUN npm install

COPY src/ ./src/
RUN npm run build

EXPOSE 3000
CMD ["node", "dist/index.js"]
