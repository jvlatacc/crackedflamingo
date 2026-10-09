'use strict';

/* flamingo capture dashboard — dependency-free poller/renderer.
 * Everything the API returns is attacker-controlled, so all dynamic text
 * goes through textContent — no innerHTML with record data, ever. */

const POLL_MS = 5000;
const SUMMARY_URL = '/api/summary';
const RECORDS_URL = '/api/records?limit=50';

/* Order decides which captured value shows in the "secret" column when a
 * record carries more than one. Mirrors the server-side secret key set. */
const SECRET_PRIORITY = ['password', 'authorization', 'pubkey', 'community', 'hashcat'];

const state = {
  lastSummary: null,   // last successful summary — kept on screen on errors
  lastRecords: null,   // last successful records page
  lastError: null,     // last poll error, for the header
  inFlight: false,
};

const $ = (id) => document.getElementById(id);

async function getJSON(url) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 3000);
  try {
    const res = await fetch(url, { signal: ctrl.signal, cache: 'no-store' });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } finally {
    clearTimeout(timer);
  }
}

/* --- header status ----------------------------------------------------- */

function setStatus(kind, text) {
  $('status-dot').className = 'dot' + (kind === 'warn' ? ' warn' : '');
  $('status-text').textContent = text;
}

/* --- rendering --------------------------------------------------------- */

function renderSummary(sum) {
  // Empty state is normal, not an error: say what is being watched.
  const empty = sum.total === 0;
  $('zones').hidden = empty;
  $('empty-state').hidden = !empty;
  if (empty) return;

  $('kpi-total').textContent = String(sum.total);
  $('kpi-protocols').textContent = String(Object.keys(sum.by_protocol || {}).length);
  $('kpi-hosts').textContent = String(sum.distinct_hosts);
  $('kpi-users').textContent = String(sum.distinct_usernames);

  renderTimeline(sum.timeline || []);
  renderRows($('protocol-list'), sum.by_protocol || {});
  renderChips($('method-chips'), sum.by_method || {});
  renderTopList($('top-users'), sum.top_usernames || [], 'username');
  renderTopList($('top-hosts'), sum.top_hosts || [], 'host');

  const skipped = $('skipped-note');
  if (sum.skipped_lines > 0) {
    skipped.textContent = `${sum.skipped_lines} malformed or blank line(s) skipped`;
    skipped.hidden = false;
  } else {
    skipped.hidden = true;
  }
}

/* Metric row: name, meter bar sized against the max, count. */
function renderRows(container, counts) {
  container.replaceChildren();
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const max = entries.length ? entries[0][1] : 0;
  for (const [name, count] of entries) {
    const row = document.createElement('div');
    row.className = 'row';

    const label = document.createElement('span');
    label.textContent = name;

    const meter = document.createElement('div');
    meter.className = 'meter';
    meter.style.width = max > 0 ? `${(count / max) * 100}%` : '0';

    const value = document.createElement('span');
    value.className = 'count';
    value.textContent = String(count);

    row.append(label, meter, value);
    container.appendChild(row);
  }
  if (!entries.length) {
    const none = document.createElement('div');
    none.className = 'muted';
    none.textContent = 'none observed yet';
    container.appendChild(none);
  }
}

function renderChips(container, counts) {
  container.replaceChildren();
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  for (const [name, count] of entries) {
    const chip = document.createElement('span');
    chip.className = 'chip';
    const strong = document.createElement('b');
    strong.textContent = String(count);
    chip.append(name, ' · ', strong);
    container.appendChild(chip);
  }
  if (!entries.length) {
    const none = document.createElement('span');
    none.className = 'muted';
    none.textContent = 'none observed yet';
    container.appendChild(none);
  }
}

/* Top-N list rows: name on the left, count on the right (no meters — the
 * wireframe keeps this list plain). */
function renderTopList(container, entries, nameKey) {
  container.replaceChildren();
  for (const e of entries) {
    const row = document.createElement('div');
    row.className = 'row';
    const label = document.createElement('span');
    label.textContent = e[nameKey];
    const spacer = document.createElement('div');
    const value = document.createElement('span');
    value.className = 'count';
    value.textContent = String(e.count);
    row.append(label, spacer, value);
    container.appendChild(row);
  }
  if (!entries.length) {
    const none = document.createElement('div');
    none.className = 'muted';
    none.textContent = 'none observed yet';
    container.appendChild(none);
  }
}

function renderTimeline(buckets) {
  const svgNS = 'http://www.w3.org/2000/svg';
  const svg = $('timeline');
  svg.replaceChildren();

  $('timeline-first').textContent = buckets.length ? buckets[0].bucket : '';
  $('timeline-last').textContent = buckets.length ? buckets[buckets.length - 1].bucket : '';
  if (!buckets.length) return;

  const W = 600, H = 120;
  const max = Math.max(...buckets.map((b) => b.count), 1);
  const step = W / buckets.length;
  const barWidth = Math.max(step - 1, 1);

  buckets.forEach((b, i) => {
    const height = (b.count / max) * (H - 10);
    const rect = document.createElementNS(svgNS, 'rect');
    rect.setAttribute('x', String(i * step));
    rect.setAttribute('y', String(H - height));
    rect.setAttribute('width', String(barWidth));
    rect.setAttribute('height', String(height));
    rect.setAttribute('rx', '1.5');
    const title = document.createElementNS(svgNS, 'title');
    title.textContent = `${b.bucket} · ${b.count}`;
    rect.appendChild(title);
    svg.appendChild(rect);
  });
}

function renderRecords(records, reveal) {
  const body = $('records-body');
  body.replaceChildren();

  for (const rec of records) {
    const tr = document.createElement('tr');

    const tdTime = document.createElement('td');
    tdTime.textContent = formatTime(rec._etime);

    const tdProto = document.createElement('td');
    const proto = document.createElement('span');
    proto.className = 'proto';
    proto.textContent = rec._proto || '—';
    tdProto.appendChild(proto);

    const tdHost = document.createElement('td');
    tdHost.textContent = rec._host || '—';

    const tdUser = document.createElement('td');
    tdUser.textContent = rec.username || '—';

    const tdSecret = document.createElement('td');
    const secretKey = SECRET_PRIORITY.find((k) => k in rec);
    if (secretKey === undefined) {
      tdSecret.textContent = '—';
    } else {
      tdSecret.textContent = rec[secretKey];
      tdSecret.className = reveal ? 'secret revealed' : 'secret';
      tdSecret.title = secretKey;
    }

    tr.append(tdTime, tdProto, tdHost, tdUser, tdSecret);
    body.appendChild(tr);
  }
}

function formatTime(etime) {
  if (!etime) return '—';
  const t = new Date(etime);
  if (Number.isNaN(t.getTime())) return '—';
  return t.toLocaleTimeString(undefined, { hour12: false });
}

function renderHints(sum) {
  const colNote = $('secret-col-note');
  const hint = $('secret-hint');
  if (sum.reveal) {
    colNote.textContent = 'secrets revealed (--reveal-secrets)';
    hint.textContent = 'Captured secrets are shown in clear text because the server runs with --reveal-secrets. Restart without the flag to mask them.';
  } else {
    colNote.textContent = 'secrets masked';
    hint.textContent = 'Passwords, keys, hashes, and community strings are masked in this view — run flamingo with --reveal-secrets to unmask them.';
  }
}

/* --- poll loop --------------------------------------------------------- */

async function poll() {
  if (state.inFlight) return; // a slow poll must not stack up
  state.inFlight = true;
  try {
    try {
      state.lastSummary = await getJSON(SUMMARY_URL);
      state.lastError = null;
    } catch (err) {
      // Keep the last data on screen; name the problem in the header and
      // keep retrying — no restart needed.
      state.lastError = err;
    }

    const sum = state.lastSummary;
    if (!sum) {
      setStatus('warn', 'dashboard API unreachable — retrying every 5 s');
      return;
    }

    if (sum.error) {
      setStatus('warn', `⚠ ${sum.error} — waiting for the source to appear`);
    } else if (state.lastError) {
      setStatus('warn', `fetch failed (${state.lastError.message}) — retrying`);
    } else if (sum.live) {
      setStatus('ok', `live · ${sum.source || 'capture log'} · poll 5 s`);
    } else {
      setStatus('ok', `watching ${sum.source || 'capture log'}`);
    }

    renderSummary(sum);
    renderHints(sum);

    if (sum.total > 0) {
      try {
        const page = await getJSON(RECORDS_URL);
        state.lastRecords = page.records || [];
      } catch {
        // keep the previous rows on a records-fetch failure
      }
      renderRecords(state.lastRecords || [], sum.reveal);
    }
  } finally {
    state.inFlight = false;
  }
}

function start() {
  poll();
  setInterval(poll, POLL_MS);
}

start();
