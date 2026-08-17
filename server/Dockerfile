# Longwave Game-Server.
#
# Zweistufiger Bau: die Werkzeugkette bleibt im Build-Image, ins Laufzeit-Image
# kommt nur das statisch gelinkte Binary. Das Ergebnis ist ein Distroless-Image
# von rund 15 MB ohne Shell, Paketmanager oder libc — was nicht drin ist, kann
# auch nicht angegriffen werden.

# ---------------------------------------------------------------------------
# Stufe 1: bauen
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Erst die Modul-Dateien: solange sich Abhaengigkeiten nicht aendern, bleibt
# diese Schicht im Cache und der Download entfaellt.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# VERSION landet im Binary (12 Factor V: der Release ist am Artefakt erkennbar).
ARG VERSION=dev

# CGO aus, damit das Binary statisch ist und ohne libc laeuft.
# -trimpath entfernt lokale Pfade, -s -w die Debug-Symbole.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/server ./cmd/server

# Die eingebetteten Skalen einmal gegenpruefen: ein kaputtes scales.json soll
# den Bau scheitern lassen, nicht erst den Pod im Cluster.
RUN /out/server --validate-scales

# ---------------------------------------------------------------------------
# Stufe 2: laufen
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
LABEL org.opencontainers.image.title="longwave-server" \
      org.opencontainers.image.description="Game-Server für Longwave Online" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/toto-b/longlongwave"

COPY --from=build /out/server /server

# nonroot ist UID 65532 im Distroless-Image.
USER nonroot:nonroot

EXPOSE 8080

# Kein Shell-Wrapper: das Binary ist PID 1 und bekommt SIGTERM direkt,
# damit das saubere Herunterfahren greift (12 Factor IX).
ENTRYPOINT ["/server"]
