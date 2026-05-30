FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npx tsc

FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
# Only install production dependencies to save RAM/space
RUN npm install --production
COPY --from=builder /app/dist ./dist

# Create a symlink to wallet if it exists in root, or assume it's mounted via volume
EXPOSE 3000

CMD ["node", "dist/index.js"]
