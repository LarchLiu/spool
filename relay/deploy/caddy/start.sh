#!/bin/sh
set -eu
: "${RELAY_PUBLIC_HOST:?Set RELAY_PUBLIC_HOST}"
: "${RELAY_ADMIN_HOST:?Set RELAY_ADMIN_HOST}"
case "${CADDY_CERTIFICATE_MODE:-individual}" in
  individual) SPOOL_CADDY_CONFIG=/etc/caddy/Caddyfile.individual ;;
  wildcard)
    : "${CLOUDFLARE_API_TOKEN:?Set CLOUDFLARE_API_TOKEN for wildcard DNS validation}"
    SPOOL_CADDY_CONFIG=/etc/caddy/Caddyfile.wildcard
    ;;
  *) echo 'CADDY_CERTIFICATE_MODE must be individual or wildcard' >&2; exit 1 ;;
esac
exec caddy run --config "$SPOOL_CADDY_CONFIG" --adapter caddyfile
