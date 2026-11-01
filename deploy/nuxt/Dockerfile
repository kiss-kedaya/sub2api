# Runtime image for the Nuxt/Nitro SSR bundle produced by `pnpm run build:ssr`.
#
# Build after the frontend build (context must contain .output):
#   docker build -f deploy/nuxt/Dockerfile -t sub2api-ssr:2.3.0 frontend
#
# The container listens on 0.0.0.0 so it can sit behind nginx or a container
# network. The Go API stays a separate process (NUXT_BACKEND_ORIGIN points at it);
# the legacy SPA is still embedded in the Go binary for rollback.
FROM node:24.22-bookworm-slim

ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000

WORKDIR /app
COPY .output ./.output

USER node
EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||3000)+'/health/frontend?live=1').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", ".output/server/index.mjs"]
