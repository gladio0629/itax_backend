# syntax=docker/dockerfile:1
# Build konteksti — REPO ILDIZI (shared/roles/roles.json ham kerak):
#   docker build -f services/backoffice/Dockerfile .
FROM node:22-alpine AS build
WORKDIR /app
COPY services/backoffice/package.json services/backoffice/package-lock.json ./
RUN npm ci
COPY services/backoffice/tsconfig.json services/backoffice/tsconfig.build.json ./
COPY services/backoffice/src ./src
RUN npm run build

# Ishlab chiqarish bog'liqliklari runtime bilan bir xil bazada o'rnatiladi
FROM node:22-slim AS deps
WORKDIR /app
COPY services/backoffice/package.json services/backoffice/package-lock.json ./
RUN npm ci --omit=dev

FROM node:22-slim
WORKDIR /app
ENV NODE_ENV=production PORT=8017 ROLES_FILE=/app/shared/roles/roles.json
COPY --from=deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY services/backoffice/package.json ./
# Rollar va ruxsatlar — yagona manba
COPY shared/roles/roles.json ./shared/roles/roles.json
USER node
EXPOSE 8017
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:'+(process.env.PORT||8017)+'/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD ["node", "dist/main.js"]
