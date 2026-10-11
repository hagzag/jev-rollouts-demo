# Builds the open-source OpenJev (MIT, github.com/SiliconLabAI/OpenJev) at a pinned commit.
FROM node:22-alpine
ARG OPENJEV_REF=a08e969c37b2e4a37f95b3426f983bd94303590c
RUN apk add --no-cache git \
 && git clone https://github.com/SiliconLabAI/OpenJev.git /app \
 && cd /app && git checkout "$OPENJEV_REF"
WORKDIR /app
RUN npm ci --no-audit --no-fund && npm run build
ENV PORT=3001
EXPOSE 3001
USER node
CMD ["npm", "start"]
