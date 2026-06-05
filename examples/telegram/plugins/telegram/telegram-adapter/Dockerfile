FROM node:24-slim
WORKDIR /app
COPY package.json tsconfig.json ./
RUN npm install
COPY src/ src/
RUN npm run build
CMD ["node", "dist/index.js"]
