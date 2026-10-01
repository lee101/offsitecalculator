import { createMap } from './map.js';

const $ = (id) => document.getElementById(id);
const state = { people: [], airports: null, results: [], selected: 0, cfg: {}, me: { signedIn: false }, chat: [] };
let view;

const store = {
  get() { try { return JSON.parse(localStorage.getItem('offsite') || 'null'); } catch { return null; } },
  set(v) { try { localStorage.setItem('offsite', JSON.stringify(v)); } catch {} },
};

async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(data.error || res.statusText), { status: res.status, data });
  return data;
}

const money = (n) => '$' + Math.round(n).toLocaleString();
const el = (tag, props = {}, ...kids) => { const e = Object.assign(document.createElement(tag), props); e.append(...kids); return e; };

function constraints() {
  const c = { max_stops: Number($('maxStops').value), objective: $('objective').value };
  if (Number($('maxFare').value) > 0) c.max_fare_usd = Number($('maxFare').value);
  if (Number($('hourly').value) > 0) c.hourly_value_usd = Number($('hourly').value);
  return c;
}

function persist() {
  store.set({ people: state.people, c: { maxFare: $('maxFare').value, maxStops: $('maxStops').value, objective: $('objective').value, hourly: $('hourly').value } });
}

async function nearestCity(lat, lng) {
  if (!state.airports) {
    try { state.airports = (await api('/api/airports')).airports; } catch { return ''; }
  }
  let best = null, bd = 1e9;
  for (const a of state.airports) {
    if (a.routes < 25) continue;
    const d = (a.lat - lat) ** 2 + ((a.lng - lng) * Math.cos(lat * Math.PI / 180)) ** 2;
    if (d < bd) { bd = d; best = a; }
  }
  return best ? `near ${best.city}, ${best.country}` : '';
}

async function addPerson(lat, lng, extra = {}) {
  const p = { name: extra.name || `Person ${state.people.length + 1}`, lat, lng, count: extra.count || 1, where: extra.where || '' };
  state.people.push(p);
  render(false);
  if (!p.where) { p.where = await nearestCity(lat, lng); renderPeople(); persist(); }
}

function renderPeople() {
  const ul = $('people');
  ul.replaceChildren(...state.people.map((p, i) => {
    const name = el('input', { value: p.name, ariaLabel: 'Name' });
    name.oninput = () => { p.name = name.value; persist(); };
    name.onchange = () => solve();
    const count = el('input', { type: 'number', min: 1, max: 500, value: p.count, ariaLabel: 'Headcount', title: 'Headcount' });
    count.onchange = () => { p.count = Math.max(1, Number(count.value) || 1); persist(); solve(); };
    const rm = el('button', { className: 'x', title: 'Remove', textContent: '×' });
    rm.onclick = () => { state.people.splice(i, 1); render(true); };
    return el('li', {}, name, count, rm, el('span', { className: 'where', textContent: p.where || `${p.lat.toFixed(2)}, ${p.lng.toFixed(2)}` }));
  }));
  const n = state.people.reduce((s, p) => s + p.count, 0);
  $('headcount').textContent = n ? `(${n})` : '';
}

function render(refit) {
  renderPeople();
  view.setPeople(state.people, (i, lat, lng) => { state.people[i].lat = lat; state.people[i].lng = lng; state.people[i].where = ''; nearestCity(lat, lng).then((w) => { state.people[i].where = w; renderPeople(); }); persist(); solve(); });
  if (refit) view.fit(state.people);
  persist();
  solve();
}

let solveTimer, solveSeq = 0;
function solve() {
  clearTimeout(solveTimer);
  solveTimer = setTimeout(async () => {
    const seq = ++solveSeq;
    if (state.people.length < 1) { state.results = []; showResults(); return; }
    try {
      const res = await api('/api/solve', { people: state.people.map(({ name, lat, lng, count }) => ({ name, lat, lng, count })), constraints: constraints() });
      if (seq !== solveSeq) return;
      state.results = res.options || [];
      state.dropped = res.dropped;
      state.selected = 0;
      showResults();
    } catch (e) { $('results').replaceChildren(el('p', { className: 'hint', textContent: e.message })); }
  }, 250);
}

function optionLabel(o) { return `${o.airport.city}, ${o.airport.country}`; }

function showResults() {
  const box = $('results');
  if (!state.results.length) {
    box.replaceChildren(el('p', { className: 'hint', textContent: state.people.length ? 'Nothing meets those limits. Raise the fare cap or allow more stops.' : 'Add at least two people.' }));
    view.showOption([], null);
    return;
  }
  box.replaceChildren(...state.results.map((o, i) => {
    const hours = Math.max(...o.legs.map((l) => l.hours_one_way));
    const head = el('h3', {}, el('span', { textContent: `${i + 1}. ${optionLabel(o)} (${o.airport.iata})` }), el('span', { textContent: money(o.total_usd) }));
    const stats = el('div', { className: 'stats' },
      el('span', { innerHTML: `avg <b>${money(o.mean_usd)}</b>` }),
      el('span', { innerHTML: `worst <b>${money(o.max_usd)}</b>` }),
      el('span', { innerHTML: `longest <b>${hours.toFixed(1)}h</b>` }));
    const card = el('div', { className: 'card' + (i === state.selected ? ' sel' : '') }, head, stats);
    if (i === state.selected) {
      const rows = o.legs.map((l) => el('tr', {}, el('td', { textContent: l.person }), el('td', { textContent: l.origin }), el('td', { textContent: money(l.fare_usd) }), el('td', { textContent: l.stops ? `${l.stops} stop${l.stops > 1 ? 's' : ''}` : 'nonstop' }), el('td', { textContent: l.hours_one_way + 'h' })));
      card.append(el('table', { className: 'legs' }, el('tr', {}, ...['Who', 'From', 'Round trip', 'Route', 'One way'].map((t) => el('th', { textContent: t }))), ...rows));
    }
    card.onclick = () => { state.selected = i; showResults(); };
    return card;
  }));
  const o = state.results[state.selected];
  const origins = o.legs.map((l) => { const a = state.airports?.find((x) => x.iata === l.origin); return a ? { lat: a.lat, lng: a.lng } : null; }).filter(Boolean);
  const draw = () => view.showOption(origins.length ? origins : state.people, { lat: o.airport.lat, lng: o.airport.lng, label: optionLabel(o) });
  if (!state.airports) api('/api/airports').then((r) => { state.airports = r.airports; showResults(); }).catch(draw); else draw();
}

async function pasteList() {
  const text = $('paste').value;
  if (!text.trim()) return;
  $('addPaste').disabled = true;
  try {
    const { places } = await api('/api/geocode', { text });
    const bad = [];
    for (const p of places) {
      if (p.resolved) state.people.push({ name: p.name, lat: p.lat, lng: p.lng, count: p.count || 1, where: p.label || '' });
      else bad.push(p.query);
    }
    $('paste').value = bad.join('\n');
    $('pasteMsg').textContent = bad.length ? `Could not find ${bad.length}: left in the box. Try "City, Country" or an airport code.` : '';
    render(true);
  } catch (e) { $('pasteMsg').textContent = e.message; }
  $('addPaste').disabled = false;
}

function renderAuth() {
  const box = $('auth');
  const me = state.me;
  if (!state.cfg.agentEnabled) { box.replaceChildren(); return; }
  if (!me.signedIn) {
    box.replaceChildren(el('a', { href: '/api/auth/login', textContent: 'Sign in with app.nz' }));
    return;
  }
  const out = el('a', { href: '#', textContent: 'Sign out' });
  out.onclick = async (e) => { e.preventDefault(); await api('/api/auth/logout', {}); await loadMe(); };
  box.replaceChildren(el('span', { textContent: me.user?.handle || me.user?.email || 'Signed in' }),
    ...(me.credits !== undefined ? [el('a', { href: me.topUpUrl, target: '_blank', rel: 'noopener', textContent: `${me.credits.toLocaleString()} credits` })] : []), out);
}

function renderGate() {
  const gate = $('agentGate'), form = $('chatForm');
  gate.replaceChildren();
  form.hidden = true;
  if (!state.cfg.agentEnabled) { gate.append(el('p', { className: 'hint', textContent: 'The AI planner is not configured on this deployment.' })); return; }
  if (!state.me.signedIn || !state.me.agentReady) {
    gate.append(el('p', { className: 'hint', textContent: `A planning agent that reads your team, tries different limits and explains the trade-offs. It runs ${state.cfg.model} on your own app.nz credits - you approve that one model and nothing else.` }),
      el('a', { href: '/api/auth/login' }, el('button', { type: 'button', textContent: 'Sign in with app.nz' })));
    return;
  }
  form.hidden = false;
}

async function loadMe() {
  try { state.me = await api('/api/me'); } catch { state.me = { signedIn: false }; }
  renderAuth(); renderGate();
}

function rich(text) {
  const frag = document.createDocumentFragment();
  text.split('\n').forEach((line, i) => {
    if (i) frag.append(document.createElement('br'));
    line.split(/\*\*(.+?)\*\*/g).forEach((part, j) => frag.append(j % 2 ? el('strong', { textContent: part }) : document.createTextNode(part)));
  });
  return frag;
}

function addMsg(role, text, tools) {
  const m = el('div', { className: 'msg ' + role }, rich(text));
  if (tools?.length) m.append(el('div', { className: 'chips' }, ...tools.map((t) => el('span', { className: 'chip', textContent: t.name + (t.error ? ' (failed)' : '') }))));
  $('chat').append(m);
  m.scrollIntoView({ block: 'nearest' });
  return m;
}

function adoptAgentResult(tools) {
  const run = [...(tools || [])].reverse().find((t) => t.name === 'find_offsite_destinations' && t.result?.options);
  if (!run) return;
  const byIata = new Map((state.airports || []).map((a) => [a.iata, a]));
  const opts = run.result.options.map((o) => {
    const a = byIata.get(o.iata);
    return a && { airport: a, total_usd: o.total_usd, mean_usd: o.mean_usd, max_usd: o.max_usd,
      legs: o.legs.map((l) => ({ person: l.person, origin: l.from, fare_usd: l.fare_usd, stops: l.stops, hours_one_way: l.hours })) };
  }).filter(Boolean);
  if (opts.length) { state.results = opts; state.selected = 0; showResults(); }
}

async function ask(e) {
  e.preventDefault();
  const text = $('chatInput').value.trim();
  if (!text) return;
  $('chatInput').value = '';
  state.chat.push({ role: 'user', content: text });
  addMsg('user', text);
  $('chatSend').disabled = true;
  $('chatStatus').textContent = 'Thinking...';
  try {
    if (!state.airports) state.airports = (await api('/api/airports')).airports;
    const res = await api('/api/agent', { messages: state.chat, people: state.people.map(({ name, lat, lng, count }) => ({ name, lat, lng, count })), constraints: constraints() });
    state.chat.push({ role: 'assistant', content: res.reply });
    addMsg('assistant', res.reply, res.tools);
    adoptAgentResult(res.tools);
    loadMe();
  } catch (err) {
    const d = err.data || {};
    const m = addMsg('err', err.message);
    if (d.code === 'insufficient_credits') m.append(' ', el('a', { href: d.topUpUrl, target: '_blank', rel: 'noopener', textContent: 'Add credits' }));
    if (d.code === 'signin_required') { m.append(' ', el('a', { href: '/api/auth/login', textContent: 'Sign in again' })); loadMe(); }
  }
  $('chatSend').disabled = false;
  $('chatStatus').textContent = '';
}

async function main() {
  const saved = store.get();
  if (saved) {
    state.people = saved.people || [];
    for (const [k, v] of Object.entries(saved.c || {})) if ($(k) && v !== undefined) $(k).value = v;
  }
  state.cfg = await api('/api/config').catch(() => ({}));
  view = await createMap($('map'), state.cfg.mapsKey, (lat, lng) => addPerson(lat, lng));
  ['maxFare', 'maxStops', 'objective', 'hourly'].forEach((id) => $(id).addEventListener('change', () => { persist(); solve(); }));
  $('addPaste').onclick = pasteList;
  $('clearAll').onclick = () => { state.people = []; $('paste').value = ''; render(false); };
  $('chatForm').onsubmit = ask;
  $('chatInput').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) $('chatForm').requestSubmit(); });
  render(true);
  await loadMe();
  if (new URLSearchParams(location.search).has('auth')) history.replaceState(null, '', location.pathname);
}

main();
