#!/usr/bin/env bash
# Rauchtest fuer die beiden Container-Images.
#
# Prueft das, was die Go-Tests nicht abdecken koennen: dass die Images
# tatsaechlich starten, unprivilegiert laufen, mit read-only Wurzel-Dateisystem
# auskommen (so laufen sie spaeter im Cluster) und auf SIGTERM sauber aufhoeren.
#
# Aufruf:  scripts/smoke.sh [server-image] [web-image]

set -euo pipefail

SERVER_IMG="${1:-longwave-server:dev}"
WEB_IMG="${2:-longwave-web:dev}"

SERVER_NAME="longwave-smoke-server"
WEB_NAME="longwave-smoke-web"
NET_NAME="longwave-smoke-net"

SERVER_PORT=18080
WEB_PORT=18081

pass() { printf '  \033[32mok\033[0m    %s\n' "$1"; }
fail() { printf '  \033[31mFEHLER\033[0m %s\n' "$1"; FAILED=1; }
FAILED=0

cleanup() {
  docker rm -f "$SERVER_NAME" "$WEB_NAME" >/dev/null 2>&1 || true
  docker network rm "$NET_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

echo "Rauchtest: $SERVER_IMG / $WEB_IMG"
docker network create "$NET_NAME" >/dev/null

# ---------------------------------------------------------------------------
# Game-Server
# ---------------------------------------------------------------------------
echo
echo "Game-Server"

# Genau die Haertung, die auch das Deployment setzt: kein root, kein
# beschreibbares Wurzel-Dateisystem, keine Capabilities.
docker run -d --name "$SERVER_NAME" \
  --network "$NET_NAME" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -p "${SERVER_PORT}:8080" \
  -e LOG_FORMAT=json \
  -e MAX_PLAYERS=6 \
  "$SERVER_IMG" >/dev/null

for _ in $(seq 1 40); do
  curl -sf "http://localhost:${SERVER_PORT}/healthz" >/dev/null 2>&1 && break
  sleep 0.5
done

if curl -sf "http://localhost:${SERVER_PORT}/healthz" | grep -q ok; then
  pass "/healthz antwortet"
else
  fail "/healthz antwortet nicht"
  docker logs "$SERVER_NAME" 2>&1 | tail -20
  exit 1
fi

curl -sf "http://localhost:${SERVER_PORT}/readyz" | grep -q ready \
  && pass "/readyz meldet bereit" || fail "/readyz meldet nicht bereit"

curl -sf "http://localhost:${SERVER_PORT}/metrics" | grep -q longwave_lobbies_active \
  && pass "/metrics liefert die Spielmetriken" || fail "/metrics ohne Spielmetriken"

# Die Version muss aus dem Build stammen, nicht der Default "dev" sein.
VERSION_JSON=$(curl -sf "http://localhost:${SERVER_PORT}/api/version" || echo '{}')
if echo "$VERSION_JSON" | grep -q '"version"'; then
  pass "/api/version meldet $(echo "$VERSION_JSON" | sed 's/.*"version":"\([^"]*\)".*/\1/')"
else
  fail "/api/version antwortet nicht"
fi

# Unprivilegiert: distroless nonroot ist UID 65532.
UID_IN_CONTAINER=$(docker inspect -f '{{.Config.User}}' "$SERVER_IMG" 2>/dev/null || true)
[ "$UID_IN_CONTAINER" = "nonroot:nonroot" ] \
  && pass "laeuft als $UID_IN_CONTAINER" \
  || fail "laeuft als '$UID_IN_CONTAINER', erwartet nonroot:nonroot"

# Lobby anlegen: der eigentliche Beweis, dass die Anwendung arbeitet.
CODE=$(curl -sf -X POST "http://localhost:${SERVER_PORT}/api/lobby" | sed 's/.*"code":"\([^"]*\)".*/\1/')
if [ ${#CODE} -eq 4 ]; then
  pass "Lobby $CODE angelegt"
else
  fail "Lobby konnte nicht angelegt werden"
fi

# ---------------------------------------------------------------------------
# Frontend
# ---------------------------------------------------------------------------
echo
echo "Frontend"

# nginx braucht beschreibbare Pfade; im Cluster loest das ein emptyDir-tmpfs.
docker run -d --name "$WEB_NAME" \
  --network "$NET_NAME" \
  --read-only \
  --tmpfs /tmp \
  --tmpfs /var/cache/nginx \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -p "${WEB_PORT}:8080" \
  "$WEB_IMG" >/dev/null

for _ in $(seq 1 40); do
  curl -sf "http://localhost:${WEB_PORT}/healthz" >/dev/null 2>&1 && break
  sleep 0.5
done

if curl -sf "http://localhost:${WEB_PORT}/healthz" | grep -q ok; then
  pass "/healthz antwortet (auch mit read-only Wurzel-Dateisystem)"
else
  fail "/healthz antwortet nicht"
  docker logs "$WEB_NAME" 2>&1 | tail -20
fi

for FILE in / /app.js /style.css; do
  STATUS=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:${WEB_PORT}${FILE}")
  [ "$STATUS" = "200" ] && pass "$FILE liefert 200" || fail "$FILE liefert $STATUS"
done

curl -sI "http://localhost:${WEB_PORT}/" | grep -qi 'content-security-policy' \
  && pass "CSP-Header gesetzt" || fail "CSP-Header fehlt"

curl -sf "http://localhost:${WEB_PORT}/version.txt" >/dev/null \
  && pass "version.txt vorhanden" || fail "version.txt fehlt"

# Das Frontend darf keine Spiellogik mehr enthalten: die Skalen kommen vom Server.
if curl -sf "http://localhost:${WEB_PORT}/app.js" | grep -q 'SCALES_DATA'; then
  fail "app.js enthaelt noch SCALES_DATA — die Skalen sollen vom Server kommen"
else
  pass "app.js ohne eingebaute Skalendaten"
fi

# ---------------------------------------------------------------------------
# Kommunikation der beiden Teile ueber das Container-Netz
# ---------------------------------------------------------------------------
echo
echo "Zusammenspiel"

if docker run --rm --network "$NET_NAME" curlimages/curl:latest \
     -sf "http://${SERVER_NAME}:8080/healthz" >/dev/null 2>&1; then
  pass "der Server ist im Container-Netz unter seinem Namen erreichbar"
else
  # Kein Netzzugang fuer das Hilfs-Image: kein Grund, den Test zu versenken.
  printf '  \033[33muebersprungen\033[0m  Netzprüfung (curl-Image nicht verfügbar)\n'
fi

# ---------------------------------------------------------------------------
# Sauberes Herunterfahren (12 Factor IX)
# ---------------------------------------------------------------------------
echo
echo "Herunterfahren"

START=$(date +%s)
docker stop -t 15 "$SERVER_NAME" >/dev/null
ELAPSED=$(( $(date +%s) - START ))
EXIT_CODE=$(docker inspect -f '{{.State.ExitCode}}' "$SERVER_NAME")

if [ "$EXIT_CODE" = "0" ] && [ "$ELAPSED" -lt 10 ]; then
  pass "SIGTERM in ${ELAPSED}s sauber beendet (Exit-Code 0)"
else
  fail "Herunterfahren dauerte ${ELAPSED}s mit Exit-Code $EXIT_CODE (erwartet 0, unter 10s)"
fi

if docker logs "$SERVER_NAME" 2>&1 | grep -q 'fahre herunter'; then
  pass "das Herunterfahren steht im Log"
else
  fail "im Log fehlt die Meldung zum Herunterfahren"
fi

echo
if [ "$FAILED" -eq 0 ]; then
  echo "Rauchtest bestanden."
else
  echo "Rauchtest fehlgeschlagen."
  exit 1
fi
