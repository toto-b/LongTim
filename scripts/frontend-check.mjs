#!/usr/bin/env node
/* Frontend-Rauchtest ohne Browser.
 *
 * Das Frontend hat absichtlich keine Abhaengigkeiten und damit auch kein
 * Test-Framework. Damit render() aber nicht voellig ungeprueft bleibt, baut
 * dieses Skript einen minimalen DOM-Ersatz, laedt web/app.js hinein und spielt
 * eine echte Runde gegen einen laufenden Server.
 *
 * Geprueft wird:
 *   - app.js laeuft ohne Ausnahme durch (Verbinden, Rendern, Phasenwechsel)
 *   - der Ratende bekommt in HINT/GUESS kein Ziel angezeigt
 *   - nach dem Aufdecken stehen Ziel und Ergebnisse im DOM
 *
 * Aufruf:  node scripts/frontend-check.mjs [http://localhost:8080]
 */

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const BASE = process.argv[2] || 'http://localhost:8080';
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

// ---------------------------------------------------------------------------
// Minimaler DOM
// ---------------------------------------------------------------------------

class ClassList {
  constructor(node) { this.node = node; this.set = new Set(); }
  add(...c) { c.forEach((x) => this.set.add(x)); }
  remove(...c) { c.forEach((x) => this.set.delete(x)); }
  contains(c) { return this.set.has(c); }
  toggle(c, force) {
    const on = force === undefined ? !this.set.has(c) : !!force;
    if (on) this.set.add(c); else this.set.delete(c);
    return on;
  }
}

class Node {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.classList = new ClassList(this);
    this.style = {};
    this.dataset = {};
    this._text = '';
    this.listeners = {};
    this.disabled = false;
    this.value = '';
    this.title = '';
  }
  set className(v) { this.classList.set = new Set(String(v).split(/\s+/).filter(Boolean)); }
  get className() { return [...this.classList.set].join(' '); }
  set textContent(v) { this._text = String(v); this.children = []; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(''); }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; this._text = ''; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  dispatch(type, ev = {}) { (this.listeners[type] || []).forEach((fn) => fn(ev)); }
  getBoundingClientRect() { return { left: 0, top: 0, width: 400, height: 400 }; }
  scrollIntoView() {}
  focus() {}
  // Alle Knoten im Teilbaum, flach.
  all() { return this.children.flatMap((c) => [c, ...c.all()]); }
}

const nodes = new Map();
const document = {
  getElementById(id) {
    if (!nodes.has(id)) nodes.set(id, new Node('div'));
    return nodes.get(id);
  },
  createElement(tag) { return new Node(tag); },
};

const store = new Map();
const sessionStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};

const url = new URL(BASE);
const location = {
  protocol: url.protocol,
  host: url.host,
  origin: url.origin,
  pathname: '/',
  search: '',
};

// ---------------------------------------------------------------------------
// app.js in diesem Kontext ausfuehren
// ---------------------------------------------------------------------------

const source = readFileSync(join(ROOT, 'web', 'app.js'), 'utf8');
// Das Frontend benutzt bewusst relative Pfade ("/api/lobby"). Der Browser loest
// die gegen seinen Origin auf, Node kann das nicht — hier nachgeholt.
const relativeFetch = (input, init) =>
  fetch(typeof input === 'string' && input.startsWith('/') ? BASE + input : input, init);

const sandbox = {
  document, sessionStorage, location, WebSocket,
  fetch: relativeFetch,
  history: { replaceState() {} },
  navigator: { clipboard: { writeText: async () => {} } },
  setTimeout, clearTimeout, setInterval, clearInterval,
  confirm: () => true,
  prompt: () => {},
  console,
  URLSearchParams,
  Object, Math, JSON, Array, String, Number, Boolean, Promise, Error, Set, Map, Date,
};

const run = new Function(...Object.keys(sandbox), `${source}\nreturn { el, snapshot: () => snapshot, send };`);
let app;
try {
  app = run(...Object.values(sandbox));
} catch (err) {
  fail(`app.js konnte nicht geladen werden: ${err.stack}`);
}

// ---------------------------------------------------------------------------
// Ablauf
// ---------------------------------------------------------------------------

const problems = [];
function check(ok, message) {
  if (!ok) problems.push(message);
}
function fail(message) {
  console.error(`FEHLER: ${message}`);
  process.exit(1);
}
async function waitFor(predicate, what, timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  // Fehlermeldungen, die das Frontend selbst angezeigt haette, mitausgeben —
  // sonst sieht man nur "Zeitueberschreitung" und nicht den Grund.
  const shown = [app?.el?.lobbyError?.textContent, app?.el?.errorLine?.textContent]
    .filter(Boolean)
    .join(' | ');
  fail(`Zeitueberschreitung beim Warten auf ${what}${shown ? ` — UI meldet: ${shown}` : ''}`);
}

// Der Renderer haengt am 'grid'-Knoten; Klicks simuliert der Test direkt.
const el = app.el;

// Lobby anlegen und als erster Spieler beitreten (ueber die echte UI-Aktion).
el.playerName.value = 'Renderer';
el.btnCreate.dispatch('click');
await waitFor(() => app.snapshot() !== null, 'den ersten Zustand vom Server');

const code = app.snapshot().lobbyCode;
check(el.codeBadge.textContent === code, 'der Lobby-Code steht nicht in der Kopfzeile');
check(!el.screenGame.classList.contains('hidden'), 'der Spielbildschirm ist nicht sichtbar');

// Zweiter Spieler ueber einen direkten WebSocket, damit eine Runde moeglich ist.
const partner = new WebSocket(
  `${url.protocol === 'https:' ? 'wss:' : 'ws:'}//${url.host}/api/ws?lobby=${code}&name=Partner`,
);
let partnerState = null;
partner.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.type === 'state') partnerState = msg.state;
});
await waitFor(() => partnerState !== null, 'den Beitritt des zweiten Spielers');
await waitFor(() => app.snapshot().players.length === 2, 'zwei Spieler im Zustand');

check(el.scoreboard.children.length === 2, 'die Spielerliste zeigt nicht zwei Chips');
check(el.btnStart.disabled === false, '"Runde starten" ist trotz zwei Spielern gesperrt');

// Runde starten.
el.btnStart.dispatch('click');
await waitFor(() => app.snapshot().phase === 'HINT', 'die Hinweisphase');

const s = app.snapshot();
const iAmClueGiver = s.youAreClueGiver;
const markerKinds = () => el.markerLayer.children.map((m) => m.className);

if (iAmClueGiver) {
  check(s.target != null, 'der Hinweisgeber hat kein Ziel erhalten');
  check(markerKinds().some((c) => c.includes('target')), 'der Zielmarker wird nicht gezeichnet');
  check(!el.hintInputRow.classList.contains('hidden'), 'das Hinweisfeld fehlt beim Hinweisgeber');
} else {
  check(s.target == null, 'der Ratende hat ein Ziel erhalten');
  check(!markerKinds().some((c) => c.includes('target')), 'beim Ratenden wird ein Zielmarker gezeichnet');
  check(el.hintInputRow.classList.contains('hidden'), 'der Ratende sieht das Hinweisfeld');
}
check(el.labelLeft.textContent.length > 1, 'die Achsenbeschriftung ist leer');
check(el.bands.children.length === 4, `${el.bands.children.length} Punktebaender in der Legende, erwartet 4`);

// Hinweis abschicken — je nachdem, wer der Hinweisgeber ist.
if (iAmClueGiver) {
  el.hintInput.value = 'Rendertest';
  el.btnHint.dispatch('click');
} else {
  partner.send(JSON.stringify({ type: 'submit_hint', hint: 'Rendertest' }));
}
await waitFor(() => app.snapshot().phase === 'GUESS', 'die Ratephase');

if (!iAmClueGiver) {
  check(el.hintText.textContent === 'Rendertest', 'der Hinweis wird nicht angezeigt');
  check(app.snapshot().target == null, 'der Ratende bekam in GUESS ein Ziel');

  // Klick ins Feld: Marker vorlaeufig, Bestaetigen wird freigegeben.
  el.grid.dispatch('click', { clientX: 200, clientY: 120 });
  check(el.btnGuess.disabled === false, '"Tipp bestätigen" bleibt nach dem Klick gesperrt');
  check(markerKinds().some((c) => c.includes('pending')), 'der vorläufige Marker fehlt');
  el.btnGuess.dispatch('click');
} else {
  partner.send(JSON.stringify({ type: 'place_guess', point: { x: 50, y: 30 } }));
}

await waitFor(() => app.snapshot().phase === 'REVEAL', 'das Aufdecken');

const final = app.snapshot();
check(final.target != null, 'nach dem Aufdecken fehlt das Ziel');
check(markerKinds().some((c) => c.includes('target')), 'nach dem Aufdecken fehlt der Zielmarker');
check(el.ringLayer.children.length === 4, `${el.ringLayer.children.length} Ringe gezeichnet, erwartet 4`);
check(!el.resultsPanel.classList.contains('hidden'), 'das Ergebnis-Panel bleibt verborgen');
check(el.resultsList.children.length >= 2, 'im Ergebnis fehlen Zeilen (Tipp + Hinweisgeber)');
check(el.historyBody.children.length === 1, 'der Verlauf hat keinen Eintrag');
check(!el.btnNext.classList.contains('hidden'), '"Nächste Runde" wird nicht angeboten');

partner.close();
app.el.btnLeave.dispatch('click');

if (problems.length > 0) {
  console.error('Frontend-Rauchtest fehlgeschlagen:');
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log('ok — Frontend rendert alle Phasen, das Ziel blieb beim Hinweisgeber');
process.exit(0);
