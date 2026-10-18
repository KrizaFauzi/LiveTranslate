FROM node:24.11.0-bookworm-slim AS build
WORKDIR /app
COPY auth-service/package*.json ./
# npm ci is used once a lockfile is available; offline authoring cannot generate one.
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY auth-service/tsconfig.json ./
COPY auth-service/src ./src
RUN npm run build

FROM node:24.11.0-bookworm-slim
ENV NODE_ENV=production
WORKDIR /app
COPY auth-service/package*.json ./
RUN if [ -f package-lock.json ]; then npm ci --omit=dev; else npm install --omit=dev; fi
COPY --from=build /app/dist ./dist
USER node
EXPOSE 3001
CMD ["node", "dist/server.js"]
