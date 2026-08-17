# Longwave Online

Wavelength auf zwei Achsen — als Online-Spiel mit Lobbies, aufgeteilt in Frontend
und Game-Server, containerisiert und auf Kubernetes deployt.

Einer sieht die geheime Zielposition und gibt einen Hinweis. Alle anderen setzen
je einen Marker. Wer nah dran liegt, bekommt Punkte; der Hinweisgeber bekommt den
Durchschnitt seiner Gruppe. Danach rotiert die Rolle.

---

## Inhalt

- [Warum überhaupt zwei Teile?](#warum-überhaupt-zwei-teile)
- [Architektur](#architektur)
- [Schnellstart](#schnellstart)
- [12-Factor-App](#12-factor-app)
- [CNCF-Technologien](#cncf-technologien)
- [Projektstruktur](#projektstruktur)
- [Tests](#tests)
- [Konfiguration](#konfiguration)
- [Bekannte Grenzen](#bekannte-grenzen)
- [Die Offline-Fassung](#die-offline-fassung)

---

## Warum überhaupt zwei Teile?

Die Aufteilung ist hier nicht konstruiert, um eine Anforderung zu erfüllen —
sie ist die Voraussetzung dafür, dass das Spiel online funktioniert.

Der Vorgänger dieses Projekts (noch im Repo, siehe
[Die Offline-Fassung](#die-offline-fassung)) lief komplett im Browser. Der ganze
Spielzustand, inklusive Zielkoordinate, lag im JavaScript jedes Mitspielers.
Geheimhaltung war Ehrensache: „Bitte jetzt kurz wegschauen."

Sobald jeder an seinem eigenen Gerät sitzt, trägt das nicht mehr. Es braucht eine
Instanz, die das Ziel kennt und **entscheidet, wer es erfahren darf**. Genau das
macht der Game-Server: Er serialisiert den Spielzustand **pro Empfänger**.

```go
// server/internal/game/view.go
func (g *Game) maySeeTarget(viewerID string) bool {
    if g.phase == PhaseReveal { return true }          // aufgedeckt: alle
    if g.phase == PhaseLobby  { return false }
    return viewerID != "" && viewerID == g.clueGiverID // sonst: nur der Hinweisgeber
}
```

Im JSON, das ein Ratender bekommt, fehlt das Feld `target` vollständig — es ist
nicht etwa versteckt oder ausgegraut, es ist nicht da. Dasselbe gilt für fremde
Tipps vor dem Aufdecken, damit niemand abschreibt. Das ist mit mehreren Tests
abgesichert, bis hinunter auf die rohen WebSocket-Frames:

```
server/internal/game/engine_test.go     TestTargetNeverLeaksToGuessers
                                        TestForeignGuessesStayHiddenUntilReveal
                                        TestHistoryDoesNotLeakTheRunningRound
server/internal/lobby/lobby_test.go     TestBroadcastRedactsPerRecipient
server/internal/transport/ws_test.go    TestWSTargetNeverAppearsInGuesserFrames
```

Der Lastgenerator (`server/cmd/loadgen`) prüft dieselbe Zusicherung im laufenden
Betrieb und bricht mit Exit-Code 1 ab, falls jemals ein Ziel bei einem Ratenden
ankommt.

---

## Architektur

```
                          ┌──────────────────────────────────────┐
   Browser ──── HTTP ────▶ │  Ingress (ingress-nginx)             │
              WebSocket    │  Host: longwave.local                │
                          └───────┬──────────────────────┬───────┘
                                  │ /api                 │ /
                                  ▼                      ▼
                   ┌──────────────────────┐   ┌──────────────────────┐
                   │  longwave-server     │   │  longwave-web        │
                   │  Go · distroless     │   │  nginx · statisch    │
                   │  1 Replica           │   │  2 Replicas          │
                   │                      │   │                      │
                   │  POST /api/lobby     │   │  index.html          │
                   │  GET  /api/ws  (WS)  │   │  app.js  style.css   │
                   │  GET  /healthz       │   │                      │
                   │  GET  /readyz        │   │                      │
                   │  GET  /metrics       │   │                      │
                   └──────────┬───────────┘   └──────────────────────┘
                              │ Skalen aus ConfigMap
                              ▼
                   ┌──────────────────────┐   ┌──────────────────────┐
                   │  ConfigMap           │   │  Prometheus ──▶ Grafana
                   │  longwave-scales     │◀──┤  scrapt /metrics alle 15s
                   └──────────────────────┘   └──────────────────────┘
                                                    ▲ ServiceMonitor
```

**Wie die beiden Teile miteinander reden.** Das Frontend kennt keine
Backend-Adresse. Es baut die WebSocket-URL zur Laufzeit aus dem eigenen Origin:

```js
// web/app.js
const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
return `${proto}//${location.host}/api/ws?${q}`;
```

Weil der Ingress beide Teile unter demselben Host ausliefert und nur nach Pfad
trennt, funktioniert dasselbe Image unverändert lokal, im kind-Cluster und
hinter jedem anderen Ingress. Keine Backend-URL zur Bauzeit, kein CORS.

**Rundenablauf** (serverseitig autoritativ, `server/internal/game/engine.go`):

```
LOBBY ──start_round──▶ HINT ──submit_hint──▶ GUESS ──alle Tipps da──▶ REVEAL
  ▲                     │                                                │
  │                     └─ redraw_scales (nur Hinweisgeber)              │
  └──────────────────────────── start_round ◀──────────────────────────-─┘
```

Jeder Zustandsübergang wird auf dem Server geprüft. Ein Client kann keine Phase
überspringen, nicht für jemand anderen tippen und als Hinweisgeber nicht
mitraten — die Regeln stehen nicht im Frontend, sondern in der Engine.

---

## Schnellstart

Voraussetzungen: Docker. Alles Weitere bringt die Nix-DevShell mit
(Go, kind, kubectl, Helm, kustomize):

```bash
nix develop          # oder: direnv allow
make help            # alle Targets
```

### Lokal, ohne Kubernetes

```bash
make dev
```

Der Game-Server läuft auf `:8080` und liefert für den Entwicklungs-Loop das
Frontend gleich mit (`STATIC_DIR`). Zwei Browserfenster auf
<http://localhost:8080> öffnen, in einem eine Lobby erstellen, im anderen mit dem
Code beitreten.

### Auf Kubernetes (kind)

```bash
make up
```

Das legt der Reihe nach an: kind-Cluster → kube-prometheus-stack (Helm) →
ingress-nginx (Helm) → Images bauen und laden → Manifeste anwenden.

Danach einmalig:

```bash
echo "127.0.0.1 longwave.local" | sudo tee -a /etc/hosts
```

Dann spielen unter <http://longwave.local>.

> **Kubeconfig:** Das Projekt schreibt in eine eigene Kubeconfig unter
> `.kube/config` und lässt `~/.kube/config` in Ruhe. Für `kubectl` außerhalb von
> `make`:
> ```bash
> eval "$(make kubeconfig)"
> ```

### Weitere Targets

| Befehl | Wirkung |
|---|---|
| `make test` | Alle Go-Tests mit Race-Detector |
| `make lint` | `gofmt`-Prüfung und `go vet` |
| `make images` | Beide Container-Images bauen |
| `make smoke` | Beide Images starten und durchprüfen |
| `make manifests` | Gerenderte Manifeste ansehen, ohne zu deployen |
| `make logs` | Logs des Game-Servers folgen |
| `make grafana` | Grafana auf <http://localhost:3000> (admin / longwave) |
| `make prometheus` | Prometheus-UI auf <http://localhost:9090> |
| `make cluster-delete` | kind-Cluster wieder wegräumen |

---

## 12-Factor-App

| # | Faktor | Umsetzung | Wo |
|---|---|---|---|
| **I** | **Codebase** | Ein Git-Repo für beide Teile, ein Deployment-Weg. | — |
| **II** | **Dependencies** | Go-Abhängigkeiten in `go.mod`/`go.sum` gepinnt. Helm-Chart-Versionen im Makefile festgelegt (`INGRESS_CHART_VERSION`, `KPS_CHART_VERSION`). Das Frontend hat bewusst **keine** Abhängigkeiten — kein npm, kein Bundler, kein CDN. | `server/go.mod`, `Makefile` |
| **III** | **Config** | Ausschließlich Environment-Variablen, kein Config-File im Image. Jeder Wert hat einen Default; ein *unbrauchbarer* gesetzter Wert lässt den Prozess gar nicht erst starten, statt still auf den Default zurückzufallen. | `server/internal/config/`, `k8s/10-configmap.yaml` |
| **IV** | **Backing Services** | Die Skalenliste ist eine austauschbare Ressource. Ohne `SCALES_PATH` nimmt der Server die eingebettete Liste, mit `SCALES_PATH` eine gemountete Datei — im Cluster eine ConfigMap. Anderer Inhalt, gleiches Image, kein Neubau. | `server/internal/scales/`, `k8s/kustomization.yaml` |
| **V** | **Build, Release, Run** | Getrennte Stufen: Multi-Stage-Dockerfile baut, `VERSION` landet per `-ldflags` im Binary und als OCI-Label im Image, das Deployment führt nur aus. Der laufende Release ist über `GET /api/version` ablesbar. | `server/Dockerfile`, `web/Dockerfile` |
| **VI** | **Processes** | Kein lokales Dateisystem als Speicher. `readOnlyRootFilesystem: true` erzwingt das technisch; nginx bekommt für seine Puffer ein `emptyDir` im RAM. | `k8s/20-server.yaml`, `k8s/30-web.yaml` |
| **VII** | **Port binding** | Das Go-Binary bringt seinen HTTP-Server selbst mit, kein externer Application-Server. Port über `PORT`, Standard 8080 (unprivilegiert). | `server/cmd/server/main.go` |
| **VIII** | **Concurrency** | Das Frontend skaliert über Replicas (2, mit PodDisruptionBudget). Der Game-Server **nicht** — bewusst und dokumentiert, siehe [Bekannte Grenzen](#bekannte-grenzen). | `k8s/30-web.yaml`, `k8s/20-server.yaml` |
| **IX** | **Disposability** | Schneller Start (kein Aufwärmen, Skalen eingebettet oder eine kleine Datei). Auf `SIGTERM`: `/readyz` sofort auf 503, damit der Ingress keine neuen Verbindungen mehr schickt, dann Abbruch der WebSocket-Schleifen und Graceful Shutdown innerhalb von `SHUTDOWN_TIMEOUT`. Der Rauchtest misst das nach. | `server/cmd/server/main.go`, `scripts/smoke.sh` |
| **X** | **Dev/Prod Parity** | Dieselben Images lokal und im Cluster, geladen per `kind load` statt über eine Registry. Das Frontend-Image enthält keine umgebungsabhängige Konfiguration, weil es seine Server-URL zur Laufzeit bildet. | `Makefile` |
| **XI** | **Logs** | Strukturiertes JSON auf stdout, über `log/slog`. Keine Logdateien, keine Rotation im Code. nginx loggt ebenfalls JSON nach stdout. `/healthz`, `/readyz` und `/metrics` sind aus dem Zugriffslog ausgenommen, sonst besteht es fast nur aus Probe-Verkehr. | `server/cmd/server/main.go`, `web/nginx.conf` |
| **XII** | **Admin Processes** | Admin-Kommandos laufen im **selben Binary** mit derselben Konfiguration, nicht als separates Skript: `--validate-scales`, `--dump-scales`, `--version`. `--validate-scales` läuft schon im Docker-Build — eine kaputte Skalendatei lässt den Bau scheitern statt später den Pod. | `server/cmd/server/main.go`, `server/Dockerfile` |

**Nicht umgesetzt:** Faktor VIII ist nur zur Hälfte erfüllt (siehe unten). Ein
externer Backing Service für Zustand (Redis) wäre der nächste Schritt, liegt
aber außerhalb des Projektumfangs.

---

## CNCF-Technologien

### Prometheus + Grafana — Hauptbestandteil

Der Game-Server exponiert `/metrics`. Ein **ServiceMonitor** (Custom Resource des
Prometheus Operators) sagt Prometheus, wo zu scrapen ist — statt einer statischen
Scrape-Konfiguration.

**Warum das hier inhaltlich passt und nicht nur ein Häkchen ist:** Die Metriken
sind fachlich, nicht bloß technisch. Die Verteilung der Rate-Abweichungen ist
gleichzeitig eine Betriebs- **und** eine Spielbalance-Metrik. Liegen fast alle
Tipps im 4-Punkte-Band, sind die Skalenpaare zu leicht; trifft kaum jemand ein
Band, sind sie zu schwer. Ein Spiel, das man nicht messen kann, kann man auch
nicht ausbalancieren.

| Metrik | Typ | Aussage |
|---|---|---|
| `longwave_lobbies_active` | Gauge | offene Lobbys |
| `longwave_players_connected` | Gauge | verbundene Spieler |
| `longwave_rounds_total` | Counter | gespielte Runden |
| `longwave_guess_distance` | Histogram | Abweichung je Tipp; Buckets auf `6/14/20/28` — genau die Punktebänder |
| `longwave_guess_points_total{points}` | Counter | wie oft welche Punktzahl vergeben wurde |
| `longwave_points_awarded_total{role}` | Counter | Punkte nach Rolle |
| `longwave_hint_seconds` | Histogram | wie lange jemand für seinen Hinweis braucht |
| `longwave_ws_messages_total{direction,type}` | Counter | Protokollverkehr |
| `longwave_ws_errors_total{reason}` | Counter | abgelehnte Kommandos |
| `longwave_lobbies_created_total` / `_reaped_total` | Counter | Lebenszyklus der Lobbys |

Das Dashboard (`monitoring/grafana-dashboard-longwave.json`, 15 Panels) wird als
ConfigMap mit dem Label `grafana_dashboard=1` ausgeliefert und vom Grafana-Sidecar
automatisch geladen — kein Import über die Oberfläche.

> **Zu `longwave_guess_points_total`:** Rechnerisch steckt diese Information schon
> im Histogram, die Bucket-Grenzen liegen ja auf den Punktebändern. Die Bänder
> daraus zurückzurechnen hieße aber, im Dashboard auf `le="6"` zu selektieren —
> und ob dieses Label als `"6"` oder `"6.0"` vorliegt, hängt an der
> Prometheus-Version. Beim Testen im Cluster war es `"6.0"`, exponiert wird `"6"`.
> Ein eigener Zähler mit selbst vergebenem Label ist davon unabhängig.

### Helm — für fremde Komponenten

Sowohl **kube-prometheus-stack** als auch **ingress-nginx** werden per Helm mit
gepinnter Chart-Version installiert; die Werte liegen versioniert im Repo
(`monitoring/kube-prometheus-values.yaml`, `ingress-nginx-values.yaml`).

**Die eigene Anwendung ist bewusst *kein* Helm-Chart.** Die Kubernetes-Manifeste
sind der prüfbare Teil dieser Aufgabe; sie hinter `{{ .Values… }}` zu verstecken
hätte den Lerneffekt verringert und nichts gewonnen. Helm kommt genau dort zum
Einsatz, wofür es gedacht ist: um fremde Komponenten zu installieren, die man
nicht selbst schreibt. Für die eigenen Manifeste übernimmt **kustomize** das
Bündeln — inklusive ConfigMap-Erzeugung aus den Originaldateien, sodass es keine
Kopien gibt, die auseinanderlaufen können.

---

## Projektstruktur

```
server/                          Teil 2: Game-Server (Go)
  cmd/server/                    Entrypoint, HTTP-Mux, Graceful Shutdown
  cmd/loadgen/                   Lastgenerator und Ende-zu-Ende-Prüfung
  internal/game/                 Spielregeln — kennt kein Netzwerk
    engine.go                    Phasen, Runden, Punkte
    view.go                      redigierter Snapshot je Empfänger
    scales.go rotation.go score.go
  internal/lobby/                Räume, Broadcast, Aufräumen
  internal/transport/            WebSocket, HTTP-Handler
  internal/protocol/             Nachrichtentypen
  internal/config/               Environment-Konfiguration
  internal/metrics/              Prometheus-Collectors
  internal/scales/scales.json    Skalenpaare (eingebettet + ConfigMap-Quelle)
  Dockerfile                     Multi-Stage → distroless, ~13 MB

web/                             Teil 1: Frontend
  index.html app.js style.css    ohne Abhängigkeiten
  nginx.conf                     statisch, non-root, CSP
  Dockerfile

k8s/                             Kubernetes-Manifeste (rohes YAML)
  00-namespace.yaml              Namespace, Pod Security "restricted"
  10-configmap.yaml              Laufzeitkonfiguration
  20-server.yaml                 Deployment + Service (Game-Server)
  30-web.yaml                    Deployment + Service + PDB (Frontend)
  40-ingress.yaml                Pfad-Routing /api und /
  50-servicemonitor.yaml         Prometheus-Anbindung
  kustomization.yaml

monitoring/                      kube-prometheus-stack: Werte + Dashboard
scripts/                         smoke.sh, frontend-check.mjs
kind-cluster.yaml                Cluster mit Port-Mapping 80/443
ingress-nginx-values.yaml
Makefile
```

Die Trennung in `internal/game` (Regeln) und `internal/transport` (Leitung) ist
Absicht: die Engine hat keine Netzwerk-Abhängigkeit und ist deshalb ohne Server
testbar.

---

## Tests

```bash
make test        # Go-Tests, Race-Detector
make smoke       # beide Container-Images
node scripts/frontend-check.mjs   # Frontend gegen einen laufenden Server
```

| Ebene | Was geprüft wird |
|---|---|
| `internal/game` | Punktebänder gegen die Werte der Offline-Fassung, Rotation (jeder genau einmal pro Durchgang, kein Doppelzug), Skalenziehung (keine Wiederholung, keine geteilten Wörter), Phasen-Übergänge, **Redaction** |
| `internal/lobby` | Broadcast liefert jedem Empfänger seinen eigenen Zustand, Lobby-Limits, Reconnect, Reaper |
| `internal/transport` | Echte WebSocket-Verbindungen gegen einen `httptest`-Server: Rollenverteilung, ganze Runde, **kein `target` in den Frames der Ratenden**, untergeschobene Spieler-IDs |
| `cmd/server` | Der komplette Handler-Stapel **inklusive Middleware** |
| `scripts/smoke.sh` | Images starten unprivilegiert und mit read-only Wurzel-Dateisystem, Health/Metrics/CSP, sauberes Herunterfahren nach `SIGTERM` |
| `scripts/frontend-check.mjs` | `app.js` in einem minimalen DOM-Ersatz: rendert alle Phasen, zeigt Ratenden kein Ziel |

Zwei Tests verdienen eine Erklärung, weil sie aus echten Fehlern entstanden sind:

- **`cmd/server/main_test.go` → `TestWebSocketUpgradeSurvivesLoggingMiddleware`.**
  Die Logging-Middleware ersetzt den `http.ResponseWriter` durch einen Recorder.
  Der verdeckte dabei den `http.Hijacker` des Originals, und jeder
  WebSocket-Upgrade endete mit `501` statt `101`. Die Tests im `transport`-Paket
  konnten das nicht sehen, weil sie die Handler ohne Middleware registrieren —
  deshalb gibt es diesen Test eine Ebene höher.
- **`internal/game/engine_test.go` → `TestHistoryDoesNotLeakTheRunningRound`.**
  Der Verlauf enthält die Ziele abgeschlossener Runden, und das ist richtig so —
  die sind aufgedeckt. Eine zu grobe Prüfung (`strings.Contains(raw, "target")`)
  schlug deshalb fälschlich Alarm. Der Test unterscheidet jetzt sauber zwischen
  „Feld auf oberster Ebene" (darf nicht) und „Verlaufseintrag einer alten Runde"
  (darf) — und prüft zusätzlich, dass die *laufende* Runde nicht im Verlauf steht.

---

## Konfiguration

Alles über Environment-Variablen, im Cluster gesetzt in `k8s/10-configmap.yaml`.

| Variable | Default | Bedeutung |
|---|---|---|
| `PORT` | `8080` | Listen-Port |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` oder `text` |
| `SCALES_PATH` | *(leer)* | Pfad zur Skalendatei; leer = eingebettete Liste |
| `LOBBY_TTL` | `30m` | Nach dieser Zeit ohne Verbindung wird eine Lobby abgeräumt |
| `MAX_PLAYERS` | `12` | Spieler je Lobby |
| `MAX_LOBBIES` | `500` | Obergrenze gegen unbegrenzten Speicherverbrauch |
| `SHUTDOWN_TIMEOUT` | `10s` | Muss unter `terminationGracePeriodSeconds` liegen |
| `ALLOWED_ORIGINS` | *(leer)* | Erlaubte Origins für den WebSocket; leer = nur gleicher Origin |
| `STATIC_DIR` | *(leer)* | **Nur für den lokalen Entwicklungs-Loop.** Liefert das Frontend vom Game-Server aus, damit beide auf demselben Origin liegen. Im Cluster leer — dort macht das nginx. |

---

## Bekannte Grenzen

**Der Game-Server läuft mit genau einer Replica — mit Absicht.**

Der Lobby-Zustand liegt im Prozessspeicher. Alle Spieler einer Lobby müssen
deshalb denselben Pod treffen. Cookie-basierte Session-Affinität am Ingress
reicht dafür **nicht**: zwei Spieler mit demselben Lobby-Code könnten auf
verschiedenen Pods landen und wären dann jeweils allein in „ihrer" Lobby ABCD.
Das wäre kein Skalierungs-Kompromiss, sondern ein Fehler.

Aus demselben Grund benutzt das Server-Deployment `strategy: Recreate` statt
einer rollierenden Aktualisierung — sonst liefen während eines Rollouts kurz zwei
Pods mit getrennten Lobby-Beständen.

Der saubere Weg wäre eines von beiden:

1. **Geteilter Zustand** — Lobbys in Redis, Server damit wirklich zustandslos.
   Das ist die 12-Factor-konforme Lösung (Faktor VI und VIII).
2. **Routing nach Lobby-Code** — jede Lobby einem Shard fest zuordnen, etwa über
   eine StatefulSet-Ordinalzahl im Lobby-Code.

Beides liegt außerhalb des Projektumfangs. Das Frontend zeigt dafür, wie der
skalierte Fall aussieht: zwei Replicas, PodDisruptionBudget, rollierendes Update
ohne Unterbrechung.

**Weiteres:**

- Ein Neustart des Servers beendet laufende Lobbys. Die Clients merken die
  Trennung, zeigen ein Banner und verbinden sich mit exponentiellem Backoff neu —
  die Lobby ist dann aber weg. Vorführbar mit
  `kubectl -n longwave delete pod -l app.kubernetes.io/name=longwave-server`.
- **Keine NetworkPolicy.** Eine wäre schnell geschrieben, aber die
  Standard-CNI von kind (kindnetd) setzt sie nicht durch. Eine Regel zu
  hinterlegen, die niemand anwendet, wäre irreführend.
- **Kein HPA.** Für den Frontend-Teil bräuchte es den metrics-server, den
  kube-prometheus-stack nicht mitbringt; für den Server wäre er wegen des
  Zustands ohnehin sinnlos.
- Das Grafana-Passwort steht im Klartext in den Helm-Werten. Für einen lokalen
  Vorführ-Cluster in Ordnung, für alles andere gehört es in ein Secret.
- Die Spieler-ID ist ein 128-Bit-Token, das der Server vergibt und der Client in
  `sessionStorage` hält. Wer ein fremdes Token errät, könnte dessen Identität
  übernehmen — praktisch ausgeschlossen, aber es ist keine echte
  Authentifizierung.

---

## Die Offline-Fassung

Der Vorgänger liegt weiter im Repo-Wurzelverzeichnis und funktioniert
unverändert — Datei im Browser öffnen, kein Server, kein Netz:

- `index.html` — 2D-Variante, Hotseat
- `classic.html` — klassische 1D-Variante
- `scales.js` — Skalenpaare

Die Spielregeln der Online-Fassung sind daraus portiert, nicht neu erfunden:

| Offline (`index.html`) | Online (Go) |
|---|---|
| `SCALES_DATA` (Zeilen 362–445) | `server/internal/scales/scales.json` |
| `SCORE_BANDS` | `internal/game/score.go` — identische Werte, per Test abgesichert |
| `pickPairs` / `pairKey` / `orient` / `sharesWord` | `internal/game/scales.go` |
| `shuffle` / `refillOrder` / `ensureOrder` / `assignRoles` | `internal/game/rotation.go` — Einheit ist jetzt *Spieler* statt *Team* |
| Phasen `SETUP → HINT_GIVEN → GUESSING → REVEALED` | `internal/game/engine.go`, serverseitig autoritativ |

Zwei bewusste Abweichungen:

- **Jeder Ratende setzt einen eigenen Marker** statt einem geteilten pro Team.
  Online ist das die natürlichere Form und liefert nebenbei eine viel
  aussagekräftigere Verteilung.
- **Der Quadranten-Gegentipp entfällt.** Er ist eine Hotseat-Mechanik gegen ein
  zweites Team am selben Tisch und ließe sich nicht sinnvoll übertragen.

Die Punkteringe liegen jetzt um das **Ziel** statt um den Tipp — bei mehreren
Tipps gleichzeitig ist das die einzige Darstellung, die für alle stimmt.
