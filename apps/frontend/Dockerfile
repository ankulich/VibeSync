# VibeSync Frontend Dockerfile.
# Multi-stage: Node builds the Vite app, nginx serves the static files and
# proxies API calls to the backend services (see nginx.conf).

# --- Build stage ---------------------------------------------------------
FROM node:20-alpine AS builder

WORKDIR /app

# Cache-friendly dependency layer (npm cache shared across rebuilds).
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --legacy-peer-deps

# Source (includes the generated TS code in src/gen).
COPY . .

ENV NODE_ENV=production
RUN npm run build

# --- Runtime stage -------------------------------------------------------
FROM nginx:alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY proxy-params.conf /etc/nginx/proxy-params.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
