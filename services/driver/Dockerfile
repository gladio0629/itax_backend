# syntax=docker/dockerfile:1
# Build konteksti: services/driver  (docker compose: build: ./services/driver)
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json tsconfig.build.json ./
COPY src ./src
RUN npm run build

# Ishlab chiqarish bog'liqliklari alohida bosqichda (native modul yo'q, lekin libc ga bog'lanmaslik uchun
# runtime bilan bir xil bazada o'rnatiladi)
FROM node:22-slim AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

FROM node:22-slim
WORKDIR /app
ENV NODE_ENV=production PORT=8011 UPLOADS_DIR=/app/uploads
COPY --from=deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./
# Lokal fayl saqlash (keyinroq S3): papka non-root foydalanuvchiga tegishli, compose volume sifatida ulaydi
RUN mkdir -p /app/uploads && chown node:node /app/uploads
USER node
EXPOSE 8011
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:'+(process.env.PORT||8011)+'/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD ["node", "dist/main.js"]
