/* Longwave Online — Frontend.
 *
 * Dieses Skript haelt bewusst KEINEN Spielzustand. Es schickt Kommandos an den
 * Server und zeichnet den Zustand, den es zurueckbekommt. Der Server liefert
 * jedem Spieler einen eigenen, redigierten Zustand — die Zielkoordinate steht
 * nur im Zustand des Hinweisgebers. Deshalb gibt es hier nichts zu verstecken:
 * was nicht angezeigt wird, ist auch nicht angekommen.
 *
 * Gegenstueck dazu ist die Offline-Fassung im Repo-Wurzelverzeichnis, in der der
 * komplette Zustand im Browser lag und die Geheimhaltung Ehrensache war.
 */
'use strict';

// ---------------------------------------------------------------------------
// Konstanten und DOM-Referenzen
// ---------------------------------------------------------------------------

const SESSION_KEY = 'longwave.session';
const RECONNECT_MIN = 500;
const RECONNECT_MAX = 10000;
const PING_INTERVAL = 20000;

const PHASE = {
  LOBBY: 'LOBBY',
  HINT: 'HINT',
  GUESS: 'GUESS',
  REVEAL: 'REVEAL',
};

const $ = (id) => document.getElementById(id);

const el = {
  banner: $('connection-banner'),
  screenLobby: $('screen-lobby'),
  screenGame: $('screen-game'),

  playerName: $('player-name'),
  joinCode: $('join-code'),
  btnCreate: $('btn-create'),
  btnJoin: $('btn-join'),
  lobbyError: $('lobby-error'),

  codeBadge: $('code-badge'),
  btnCopyLink: $('btn-copy-link'),
  roundBadge: $('round-badge'),
  scoreboard: $('scoreboard'),

  grid: $('grid'),
  ringLayer: $('ring-layer'),
  markerLayer: $('marker-layer'),
  labelTop: $('label-top'),
  labelBottom: $('label-bottom'),
  labelLeft: $('label-left'),
  labelRight: $('label-right'),
  bands: $('bands'),

  statusText: $('status-text'),
  roleText: $('role-text'),
  errorLine: $('error-line'),

  hintInputRow: $('hint-input-row'),
  hintInput: $('hint-input'),
  hintDisplay: $('hint-display'),
  hintText: $('hint-text'),

  btnStart: $('btn-start'),
  btnRedraw: $('btn-redraw'),
  btnHint: $('btn-hint'),
  btnGuess: $('btn-guess'),
  btnReveal: $('btn-reveal'),
  btnNext: $('btn-next'),
  btnReset: $('btn-reset'),
  btnLeave: $('btn-leave'),

  resultsPanel: $('results-panel'),
  resultsList: $('results-list'),
  historyBody: $('history-body'),
  footerNote: $('footer-note'),
};

// ---------------------------------------------------------------------------
// Laufzeitzustand des Clients (nicht des Spiels)
// ---------------------------------------------------------------------------

let ws = null;
let snapshot = null;

// pendingGuess ist der noch nicht bestaetigte Marker. Der Server erfaehrt ihn
// erst beim Bestaetigen — so bleibt die Sperre gegen Fehlklicks aus der
// Offline-Fassung erhalten.
let pendingGuess = null;
let guessSubmitted = false;

let reconnectDelay = RECONNECT_MIN;
let reconnectTimer = null;
let pingTimer = null;
let leaving = false;
let lastRoundKey = '';

// ---------------------------------------------------------------------------
// Session: ueberlebt einen Reload im selben Tab
// ---------------------------------------------------------------------------

function loadSession() {
  try {
    return JSON.parse(sessionStorage.getItem(SESSION_KEY)) || {};
  } catch {
    return {};
  }
}

function saveSession(patch) {
  const next = Object.assign(loadSession(), patch);
  try {
    sessionStorage.setItem(SESSION_KEY, JSON.stringify(next));
  } catch {
    // Privater Modus ohne Storage: dann eben kein Reconnect. Kein Grund abzubrechen.
  }
  return next;
}

function clearLobbyFromSession() {
  const s = loadSession();
  delete s.lobby;
  delete s.playerId;
  try {
    sessionStorage.setItem(SESSION_KEY, JSON.stringify(s));
  } catch {
    /* siehe oben */
  }
}

// ---------------------------------------------------------------------------
// Serveradressen — immer relativ zum eigenen Origin.
// Dadurch braucht das Image keine Backend-URL zum Bauzeitpunkt: lokal, im
// kind-Cluster und hinter einem Ingress funktioniert derselbe Build.
// ---------------------------------------------------------------------------

function wsURL(code, name, playerId) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const q = new URLSearchParams({ lobby: code });
  if (name) q.set('name', name);
  if (playerId) q.set('pid', playerId);
  return `${proto}//${location.host}/api/ws?${q}`;
}

async function createLobby() {
  const res = await fetch('/api/lobby', { method: 'POST' });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `Server antwortete mit ${res.status}`);
  return body.code;
}

async function lobbyExists(code) {
  const res = await fetch(`/api/lobby?lobby=${encodeURIComponent(code)}`);
  return res.ok;
}

// ---------------------------------------------------------------------------
// Verbindung
// ---------------------------------------------------------------------------

function connect(code, name) {
  leaving = false;
  clearTimeout(reconnectTimer);

  const session = saveSession({ lobby: code, name });
  ws = new WebSocket(wsURL(code, name, session.playerId));

  ws.addEventListener('open', () => {
    reconnectDelay = RECONNECT_MIN;
    el.banner.classList.add('hidden');
    clearInterval(pingTimer);
    // Haelt die Verbindung durch Proxies offen, die untaetige Sockets kappen.
    pingTimer = setInterval(() => send({ type: 'ping' }), PING_INTERVAL);
  });

  ws.addEventListener('message', (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    if (msg.type === 'state' && msg.state) {
      onState(msg.state);
    } else if (msg.type === 'error') {
      showError(msg.error);
    }
  });

  ws.addEventListener('close', () => {
    clearInterval(pingTimer);
    if (leaving) return;
    el.banner.classList.remove('hidden');
    scheduleReconnect(code, name);
  });

  // 'error' laeuft immer in ein 'close'; das Neuverbinden haengt dort.
  ws.addEventListener('error', () => {});
}

function scheduleReconnect(code, name) {
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX);
    connect(code, name);
  }, reconnectDelay);
}

function disconnect() {
  leaving = true;
  clearTimeout(reconnectTimer);
  clearInterval(pingTimer);
  if (ws && ws.readyState <= WebSocket.OPEN) ws.close(1000, 'leave');
  ws = null;
}

function send(msg) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

// ---------------------------------------------------------------------------
// Zustandsverarbeitung
// ---------------------------------------------------------------------------

function onState(state) {
  // Die Spieler-ID kommt vom Server; ab jetzt uebersteht sie einen Reload.
  if (state.you) saveSession({ playerId: state.you });

  // Bei Rundenwechsel oder Phasenwechsel die lokalen UI-Reste wegwerfen.
  const key = `${state.round}|${state.phase}`;
  if (key !== lastRoundKey) {
    lastRoundKey = key;
    if (state.phase !== PHASE.GUESS) {
      pendingGuess = null;
      guessSubmitted = false;
    }
  }
  if (state.yourGuess) guessSubmitted = true;

  snapshot = state;
  showScreen('game');
  render(state);
}

function showScreen(which) {
  el.screenLobby.classList.toggle('hidden', which !== 'lobby');
  el.screenGame.classList.toggle('hidden', which !== 'game');
}

function showError(message) {
  el.errorLine.textContent = message || '';
  el.lobbyError.textContent = el.screenLobby.classList.contains('hidden') ? '' : (message || '');
  if (message) {
    clearTimeout(showError.timer);
    showError.timer = setTimeout(() => {
      el.errorLine.textContent = '';
    }, 6000);
  }
}

// ---------------------------------------------------------------------------
// Rendern
// ---------------------------------------------------------------------------

function render(s) {
  el.codeBadge.textContent = s.lobbyCode;
  el.roundBadge.textContent = s.round > 0 ? `Runde ${s.round}` : 'Noch keine Runde';

  renderAxes(s);
  renderScoreboard(s);
  renderBands(s);
  renderMarkers(s);
  renderHint(s);
  renderStatus(s);
  renderControls(s);
  renderResults(s);
  renderHistory(s);

  el.footerNote.textContent = s.youAreClueGiver
    ? 'Du bist Hinweisgeber — nur dein Browser kennt die Zielposition.'
    : 'Das Ziel kennt in dieser Runde nur der Hinweisgeber.';
}

function renderAxes(s) {
  const x = s.axisX || ['·', '·'];
  const y = s.axisY || ['·', '·'];
  el.labelLeft.textContent = x[0];
  el.labelRight.textContent = x[1];
  el.labelTop.textContent = y[0];
  el.labelBottom.textContent = y[1];
}

function renderScoreboard(s) {
  el.scoreboard.replaceChildren();
  const sorted = [...s.players].sort((a, b) => b.score - a.score || a.name.localeCompare(b.name));

  for (const p of sorted) {
    const chip = document.createElement('div');
    chip.className = 'player-chip';
    if (p.isClueGiver) chip.classList.add('is-clue-giver');
    else if (p.isNext) chip.classList.add('is-next');
    if (p.isYou) chip.classList.add('is-you');
    if (!p.connected) chip.classList.add('offline');

    const name = document.createElement('span');
    name.textContent = p.name + (p.isYou ? ' (du)' : '');
    chip.append(name);

    let role = '';
    if (p.isClueGiver) role = 'Hinweis';
    else if (p.isNext) role = 'als nächstes';
    else if (!p.connected) role = 'offline';
    if (role) {
      const r = document.createElement('span');
      r.className = 'role';
      r.textContent = role;
      chip.append(r);
    }

    // Haken zeigt: Tipp liegt vor. Wohin getippt wurde, steht nicht im Zustand.
    if (s.phase === PHASE.GUESS && p.hasGuessed && !p.isClueGiver) {
      const tick = document.createElement('span');
      tick.className = 'tick';
      tick.textContent = '✓';
      tick.title = 'Tipp abgegeben';
      chip.append(tick);
    }

    const score = document.createElement('span');
    score.className = 'score';
    score.textContent = p.score;
    chip.append(score);

    el.scoreboard.append(chip);
  }
}

function renderBands(s) {
  el.bands.replaceChildren();
  for (const b of s.scoreBands || []) {
    const wrap = document.createElement('span');
    wrap.className = 'band';
    const sw = document.createElement('span');
    sw.className = 'swatch';
    sw.style.background = b.fill;
    sw.style.borderColor = b.stroke;
    const txt = document.createElement('span');
    txt.textContent = `${b.points} Pkt`;
    wrap.append(sw, txt);
    el.bands.append(wrap);
  }
}

function renderMarkers(s) {
  el.markerLayer.replaceChildren();
  el.ringLayer.replaceChildren();

  const clickable = s.phase === PHASE.GUESS && !s.youAreClueGiver && !guessSubmitted;
  el.grid.classList.toggle('clickable', clickable);

  // Ringe liegen jetzt um das ZIEL, nicht um den Tipp wie in der Offline-Fassung:
  // dort gab es genau einen Tipp, hier mehrere. Um das Ziel gezeichnet zeigen die
  // Ringe fuer alle Tipps gleichzeitig, welches Band sie getroffen haben.
  if (s.phase === PHASE.REVEAL && s.target) {
    const bands = [...(s.scoreBands || [])].sort((a, b) => b.max - a.max);
    for (const b of bands) {
      const ring = document.createElement('div');
      ring.className = 'ring';
      ring.style.left = `${s.target.x}%`;
      ring.style.top = `${s.target.y}%`;
      ring.style.width = `${b.max * 2}%`;
      ring.style.height = `${b.max * 2}%`;
      ring.style.background = b.fill;
      ring.style.borderColor = b.stroke;
      el.ringLayer.append(ring);
    }
  }

  if (s.target) el.markerLayer.append(marker('target', s.target, 'Ziel'));

  if (s.phase === PHASE.REVEAL) {
    for (const r of s.results || []) {
      const own = r.playerId === s.you;
      el.markerLayer.append(marker(own ? 'own' : 'other', r.point, `${r.name} · ${r.points}`));
    }
  } else {
    // Vor dem Aufdecken sieht jeder nur den eigenen Marker.
    const point = pendingGuess || s.yourGuess;
    if (point) {
      const m = marker('own', point, guessSubmitted ? 'abgegeben' : 'nicht bestätigt');
      if (!guessSubmitted) m.classList.add('pending');
      el.markerLayer.append(m);
    }
  }
}

function marker(kind, point, label) {
  const m = document.createElement('div');
  m.className = `marker ${kind}`;
  m.style.left = `${point.x}%`;
  m.style.top = `${point.y}%`;
  if (label) {
    const tag = document.createElement('span');
    tag.className = 'tag';
    tag.textContent = label;
    m.append(tag);
  }
  return m;
}

function renderHint(s) {
  const giverTyping = s.phase === PHASE.HINT && s.youAreClueGiver;
  el.hintInputRow.classList.toggle('hidden', !giverTyping);

  const showHint = s.phase !== PHASE.LOBBY && s.phase !== PHASE.HINT && !!s.hint;
  el.hintDisplay.classList.toggle('hidden', !showHint);
  if (showHint) el.hintText.textContent = s.hint;

  // Das Feld nur beim Phasenwechsel leeren, sonst wuerde jedes Broadcast die
  // Eingabe des Hinweisgebers ueberschreiben, waehrend er tippt.
  if (giverTyping && el.hintInput.dataset.round !== String(s.round)) {
    el.hintInput.dataset.round = String(s.round);
    el.hintInput.value = '';
    el.hintInput.focus();
  }
}

function renderStatus(s) {
  const clueGiver = s.players.find((p) => p.isClueGiver);
  const name = clueGiver ? clueGiver.name : '—';
  const connected = s.players.filter((p) => p.connected).length;

  let status;
  switch (s.phase) {
    case PHASE.LOBBY:
      status = connected < s.minPlayers
        ? `Warte auf Mitspieler — mindestens ${s.minPlayers} nötig (${connected} da).`
        : 'Alle da. Wer mag, startet die Runde.';
      break;
    case PHASE.HINT:
      status = s.youAreClueGiver
        ? 'Du siehst das Ziel. Gib einen Hinweis, der genau dorthin führt.'
        : `${name} sucht einen Hinweis …`;
      break;
    case PHASE.GUESS:
      if (s.youAreClueGiver) {
        status = s.pending > 0
          ? `Warte auf ${s.pending} ${s.pending === 1 ? 'Tipp' : 'Tipps'} …`
          : 'Alle Tipps sind da.';
      } else if (guessSubmitted) {
        status = s.pending > 0
          ? `Tipp abgegeben. Noch ${s.pending} offen.`
          : 'Tipp abgegeben.';
      } else {
        status = 'Klicke ins Feld und bestätige deinen Tipp.';
      }
      break;
    case PHASE.REVEAL:
      status = 'Aufgedeckt.';
      break;
    default:
      status = '';
  }
  el.statusText.textContent = status;

  const next = s.players.find((p) => p.isNext);
  el.roleText.textContent = s.phase === PHASE.LOBBY
    ? `${connected} verbunden`
    : `Hinweisgeber: ${name}${next ? ` · als nächstes: ${next.name}` : ''}`;
}

function renderControls(s) {
  const connected = s.players.filter((p) => p.connected).length;
  const canStart = connected >= s.minPlayers;
  const inLobby = s.phase === PHASE.LOBBY;

  toggle(el.btnStart, inLobby, !canStart);
  toggle(el.btnRedraw, s.phase === PHASE.HINT && s.youAreClueGiver, false);
  toggle(el.btnHint, s.phase === PHASE.HINT && s.youAreClueGiver, false);
  toggle(
    el.btnGuess,
    s.phase === PHASE.GUESS && !s.youAreClueGiver && !guessSubmitted,
    !pendingGuess,
  );
  toggle(
    el.btnReveal,
    s.phase === PHASE.GUESS && s.youAreClueGiver,
    (s.players.filter((p) => p.hasGuessed).length === 0),
  );
  toggle(el.btnNext, s.phase === PHASE.REVEAL, !canStart);
}

function toggle(button, visible, disabled) {
  button.classList.toggle('hidden', !visible);
  button.disabled = !!disabled;
}

function renderResults(s) {
  const show = s.phase === PHASE.REVEAL && (s.results || []).length > 0;
  el.resultsPanel.classList.toggle('hidden', !show);
  if (!show) return;

  el.resultsList.replaceChildren();
  const rows = [...s.results].sort((a, b) => b.points - a.points);
  for (const r of rows) {
    const row = document.createElement('div');
    row.className = 'result-row';

    const left = document.createElement('span');
    left.textContent = r.name;

    const right = document.createElement('span');
    const pts = document.createElement('span');
    pts.className = 'points';
    pts.textContent = `${r.points} Pkt`;
    const dist = document.createElement('span');
    dist.className = 'distance';
    dist.textContent = ` (Abweichung ${Math.round(r.distance)})`;
    right.append(pts, dist);

    row.append(left, right);
    el.resultsList.append(row);
  }

  const clueGiver = s.players.find((p) => p.isClueGiver);
  if (clueGiver) {
    const row = document.createElement('div');
    row.className = 'result-row';
    const left = document.createElement('span');
    left.textContent = `${clueGiver.name} (Hinweis)`;
    const right = document.createElement('span');
    right.className = 'points';
    right.textContent = `${s.clueGiverPoints || 0} Pkt`;
    row.append(left, right);
    el.resultsList.append(row);
  }
}

function renderHistory(s) {
  el.historyBody.replaceChildren();
  const rows = [...(s.history || [])].reverse();
  for (const h of rows) {
    const tr = document.createElement('tr');

    const num = document.createElement('td');
    num.textContent = h.round;

    const who = document.createElement('td');
    who.textContent = h.clueGiver || '—';

    const hint = document.createElement('td');
    hint.textContent = h.hint || '(mündlich)';
    const scales = document.createElement('div');
    scales.className = 'scales';
    scales.textContent = `${h.axisX[0]}/${h.axisX[1]} · ${h.axisY[0]}/${h.axisY[1]}`;
    hint.append(scales);

    const pts = document.createElement('td');
    pts.textContent = (h.results || [])
      .map((r) => `${r.name}: ${r.points}`)
      .join(', ') || '—';

    tr.append(num, who, hint, pts);
    el.historyBody.append(tr);
  }
}

// ---------------------------------------------------------------------------
// Eingaben
// ---------------------------------------------------------------------------

el.grid.addEventListener('click', (ev) => {
  if (!snapshot || snapshot.phase !== PHASE.GUESS) return;
  if (snapshot.youAreClueGiver || guessSubmitted) return;

  const rect = el.grid.getBoundingClientRect();
  pendingGuess = {
    x: clamp(Math.round(((ev.clientX - rect.left) / rect.width) * 100)),
    y: clamp(Math.round(((ev.clientY - rect.top) / rect.height) * 100)),
  };
  render(snapshot);
});

function clamp(v) {
  return Math.max(0, Math.min(100, v));
}

el.btnStart.addEventListener('click', () => send({ type: 'start_round' }));
el.btnNext.addEventListener('click', () => send({ type: 'start_round' }));
el.btnRedraw.addEventListener('click', () => send({ type: 'redraw_scales' }));
el.btnReveal.addEventListener('click', () => send({ type: 'reveal' }));

el.btnHint.addEventListener('click', submitHint);
el.hintInput.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter') submitHint();
});

function submitHint() {
  send({ type: 'submit_hint', hint: el.hintInput.value });
}

el.btnGuess.addEventListener('click', () => {
  if (!pendingGuess) return;
  send({ type: 'place_guess', point: pendingGuess });
  guessSubmitted = true;
  render(snapshot);
});

el.btnReset.addEventListener('click', () => {
  if (confirm('Punkte und Verlauf für alle in dieser Lobby zurücksetzen?')) {
    send({ type: 'reset' });
  }
});

el.btnLeave.addEventListener('click', () => {
  disconnect();
  clearLobbyFromSession();
  snapshot = null;
  el.banner.classList.add('hidden');
  history.replaceState(null, '', location.pathname);
  showScreen('lobby');
});

el.btnCopyLink.addEventListener('click', async () => {
  if (!snapshot) return;
  const link = `${location.origin}${location.pathname}?lobby=${snapshot.lobbyCode}`;
  try {
    await navigator.clipboard.writeText(link);
    flashButton(el.btnCopyLink, 'Kopiert!');
  } catch {
    // Ohne HTTPS gibt es keine Clipboard-API — dann den Link zeigen.
    prompt('Einladungslink:', link);
  }
});

function flashButton(button, text) {
  const original = button.textContent;
  button.textContent = text;
  setTimeout(() => {
    button.textContent = original;
  }, 1500);
}

// --- Lobby-Bildschirm ---

el.btnCreate.addEventListener('click', async () => {
  el.lobbyError.textContent = '';
  el.btnCreate.disabled = true;
  try {
    const code = await createLobby();
    enterLobby(code);
  } catch (err) {
    el.lobbyError.textContent = `Lobby konnte nicht erstellt werden: ${err.message}`;
  } finally {
    el.btnCreate.disabled = false;
  }
});

el.btnJoin.addEventListener('click', joinFromInput);
el.joinCode.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter') joinFromInput();
});
el.playerName.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter') el.joinCode.focus();
});

async function joinFromInput() {
  const code = el.joinCode.value.trim().toUpperCase();
  el.lobbyError.textContent = '';
  if (code.length !== 4) {
    el.lobbyError.textContent = 'Ein Lobby-Code hat vier Zeichen.';
    return;
  }
  el.btnJoin.disabled = true;
  try {
    if (!(await lobbyExists(code))) {
      el.lobbyError.textContent = `Es gibt keine Lobby ${code}.`;
      return;
    }
    enterLobby(code);
  } catch (err) {
    el.lobbyError.textContent = `Server nicht erreichbar: ${err.message}`;
  } finally {
    el.btnJoin.disabled = false;
  }
}

function enterLobby(code) {
  const name = el.playerName.value.trim();
  saveSession({ name });
  // Neue Lobby heisst neue Identitaet: die alte Spieler-ID gehoert zur alten Lobby.
  if (loadSession().lobby !== code) saveSession({ playerId: '' });
  history.replaceState(null, '', `${location.pathname}?lobby=${code}`);
  connect(code, name);
}

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------

function boot() {
  const session = loadSession();
  if (session.name) el.playerName.value = session.name;

  const fromURL = (new URLSearchParams(location.search).get('lobby') || '').toUpperCase();

  // Reload in derselben Lobby: direkt wieder rein, die Spieler-ID liegt bereit.
  if (session.lobby && (!fromURL || fromURL === session.lobby)) {
    showScreen('game');
    connect(session.lobby, session.name || '');
    return;
  }

  showScreen('lobby');
  if (fromURL) {
    el.joinCode.value = fromURL;
    // Ohne Namen zuerst nach dem Namen fragen, sonst direkt rein.
    if (session.name) joinFromInput();
    else el.playerName.focus();
  } else {
    el.playerName.focus();
  }
}

boot();
