#!/bin/bash
# WuKongIM API Health Check
# Created by DevBot for E2E pipeline verification
# Note: In container environments, set WUKONGIM_API_HOST to container name
#       or use `docker inspect` to get dynamic IP (see healthcheck.sh)

set -e

API_HOST="${WUKONGIM_API_HOST:-localhost}"
API_PORT="${WUKONGIM_API_PORT:-5001}"
TOKEN="${WUKONGIM_TOKEN:?ERROR: WUKONGIM_TOKEN not set}"

echo "🔍 Checking WuKongIM API at ${API_HOST}:${API_PORT}..."

# Check /route endpoint with timeout
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  --connect-timeout 5 --max-time 10 \
  -H "token: ${TOKEN}" \
  "http://${API_HOST}:${API_PORT}/route?uid=healthcheck")

if [ -z "$HTTP_CODE" ]; then
  echo "❌ WuKongIM API unreachable (curl failed)"
  exit 1
fi

if [ "$HTTP_CODE" = "200" ]; then
  echo "✅ WuKongIM API is healthy (HTTP $HTTP_CODE)"
  exit 0
else
  echo "❌ WuKongIM API check failed (HTTP $HTTP_CODE)"
  exit 1
fi
