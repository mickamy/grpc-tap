const events = [];
let selectedIdx = -1;
let filterText = '';
let autoScroll = true;
let viewMode = 'events';
let statsSortKey = 'total';
let statsSortAsc = false;
let selectedStatsMethod = null;
let paused = false;

const tbody = document.getElementById('tbody');
const tableWrap = document.getElementById('table-wrap');
const statsWrap = document.getElementById('stats-wrap');
const statsTbody = document.getElementById('stats-tbody');
const statsEl = document.getElementById('stats');
const statusEl = document.getElementById('status');
const filterEl = document.getElementById('filter');
const detailEl = document.getElementById('detail');
const statsDetailEl = document.getElementById('stats-detail');
const replayOutput = document.getElementById('replay-output');

filterEl.addEventListener('input', () => {
  filterText = filterEl.value;
  render();
});

tableWrap.addEventListener('scroll', () => {
  const el = tableWrap;
  autoScroll = el.scrollTop + el.clientHeight >= el.scrollHeight - 20;
});

// Stats sort header clicks
document.querySelectorAll('#stats-wrap th.sortable').forEach(th => {
  th.addEventListener('click', () => {
    const key = th.dataset.sort;
    if (statsSortKey === key) {
      statsSortAsc = !statsSortAsc;
    } else {
      statsSortKey = key;
      statsSortAsc = false;
    }
    document.querySelectorAll('#stats-wrap th.sortable').forEach(h => h.classList.remove('active'));
    th.classList.add('active');
    renderStats();
  });
});

function switchView(mode) {
  viewMode = mode;
  document.getElementById('tab-events').classList.toggle('active', mode === 'events');
  document.getElementById('tab-stats').classList.toggle('active', mode === 'stats');
  tableWrap.style.display = mode === 'events' ? '' : 'none';
  statsWrap.style.display = mode === 'stats' ? '' : 'none';
  if (mode === 'events') {
    detailEl.className = selectedIdx >= 0 ? 'open' : '';
    statsDetailEl.className = '';
  } else {
    detailEl.className = '';
    statsDetailEl.className = selectedStatsMethod ? 'open' : '';
  }
  render();
}

let renderPending = false;
function render() {
  if (renderPending) return;
  renderPending = true;
  requestAnimationFrame(() => {
    renderPending = false;
    if (viewMode === 'events') {
      renderTable();
    } else {
      renderStats();
    }
  });
}

// Filter parsing
const RE_DURATION = /^d([><])(\d+(?:\.\d+)?)(us|µs|ms|s|m)$/;

function parseFilterTokens(input) {
  if (!input.trim()) return [];
  const tokens = input.trim().split(/\s+/);
  return tokens.map(tok => {
    const dm = RE_DURATION.exec(tok);
    if (dm) {
      const op = dm[1];
      const val = parseFloat(dm[2]);
      const unit = dm[3];
      let ms;
      switch (unit) {
        case 'us': case 'µs': ms = val / 1000; break;
        case 'ms': ms = val; break;
        case 's': ms = val * 1000; break;
        case 'm': ms = val * 60000; break;
        default: ms = val;
      }
      return {kind: 'duration', op, ms};
    }
    if (tok.toLowerCase() === 'error') return {kind: 'error'};
    return {kind: 'text', text: tok.toLowerCase()};
  });
}

function matchesFilter(ev, cond) {
  switch (cond.kind) {
    case 'duration':
      return cond.op === '>' ? ev.duration_ms > cond.ms : ev.duration_ms < cond.ms;
    case 'error':
      return ev.status !== 0;
    case 'text':
      return (ev.method || '').toLowerCase().includes(cond.text) ||
             (ev.call_type || '').toLowerCase().includes(cond.text) ||
             (ev.protocol || '').toLowerCase().includes(cond.text) ||
             (ev.error && ev.error.toLowerCase().includes(cond.text));
  }
  return false;
}

function getFiltered() {
  const conds = parseFilterTokens(filterText);
  if (conds.length === 0) return events.map((ev, i) => ({ev, idx: i}));
  return events.reduce((acc, ev, i) => {
    if (conds.every(c => matchesFilter(ev, c))) acc.push({ev, idx: i});
    return acc;
  }, []);
}

function fmtDur(ms) {
  if (ms < 1) return (ms * 1000).toFixed(0) + '\u00b5s';
  if (ms < 1000) return ms.toFixed(1) + 'ms';
  return (ms / 1000).toFixed(2) + 's';
}

function fmtTime(iso) {
  const d = new Date(iso);
  return d.toLocaleTimeString('en-GB', {hour12: false}) + '.' + String(d.getMilliseconds()).padStart(3, '0');
}

function escapeHTML(s) {
  const el = document.createElement('span');
  el.textContent = s;
  return el.innerHTML;
}

function statusString(status) {
  if (status === 0) return 'OK';
  return 'ERR(' + status + ')';
}

function renderTable() {
  const filtered = getFiltered();
  const hasFilter = filterText.trim().length > 0;
  const pauseLabel = paused ? ' (paused)' : '';
  const eventCount = hasFilter
    ? filtered.length + '/' + events.length
    : String(events.length);
  statsEl.textContent = `${eventCount} calls${pauseLabel}`;

  const fragment = document.createDocumentFragment();
  for (const {ev, idx} of filtered) {
    const tr = document.createElement('tr');
    tr.className = 'row' +
      (idx === selectedIdx ? ' selected' : '') +
      (ev.status !== 0 ? ' has-error' : '');
    tr.dataset.idx = idx;
    tr.onclick = () => selectRow(idx);
    const statusClass = ev.status === 0 ? 'status-ok' : 'status-err';
    tr.innerHTML =
      `<td class="col-time">${escapeHTML(fmtTime(ev.start_time))}</td>` +
      `<td class="col-method" title="${escapeHTML(ev.method)}">${escapeHTML(ev.method)}</td>` +
      `<td class="col-type">${escapeHTML(ev.call_type)}</td>` +
      `<td class="col-dur">${escapeHTML(fmtDur(ev.duration_ms))}</td>` +
      `<td class="col-status"><span class="${statusClass}">${escapeHTML(statusString(ev.status))}</span></td>`;
    fragment.appendChild(tr);
  }
  tbody.replaceChildren(fragment);

  if (autoScroll && selectedIdx < 0) {
    tableWrap.scrollTop = tableWrap.scrollHeight;
  }
}

// --- Stats view ---

function buildStats() {
  const groups = new Map();
  const textConds = parseFilterTokens(filterText).filter(c => c.kind === 'text');
  for (const ev of events) {
    const method = ev.method;
    if (!method) continue;
    if (textConds.length > 0 && !textConds.every(c => method.toLowerCase().includes(c.text))) continue;
    let group = groups.get(method);
    if (!group) {
      group = {method, durations: [], errors: 0};
      groups.set(method, group);
    }
    group.durations.push(ev.duration_ms);
    if (ev.status !== 0) group.errors++;
  }
  const rows = [];
  for (const g of groups.values()) {
    const durs = g.durations.sort((a, b) => a - b);
    const count = durs.length;
    const total = durs.reduce((s, d) => s + d, 0);
    const avg = total / count;
    rows.push({method: g.method, count, errors: g.errors, avg, total});
  }
  return rows;
}

function sortStats(rows) {
  const dir = statsSortAsc ? 1 : -1;
  rows.sort((a, b) => {
    let va, vb;
    if (statsSortKey === 'errors') {
      va = a.count > 0 ? a.errors / a.count : 0;
      vb = b.count > 0 ? b.errors / b.count : 0;
    } else {
      va = a[statsSortKey];
      vb = b[statsSortKey];
    }
    if (va < vb) return -1 * dir;
    if (va > vb) return 1 * dir;
    return 0;
  });
}

function renderStats() {
  const rows = buildStats();
  sortStats(rows);
  statsEl.textContent = `${rows.length} methods`;

  const fragment = document.createDocumentFragment();
  for (const r of rows) {
    const tr = document.createElement('tr');
    tr.className = 'row' + (selectedStatsMethod === r.method ? ' selected' : '');
    tr.onclick = () => selectStatsRow(r);
    const errStr = r.errors > 0
      ? `<span class="status-err">${r.errors}(${(r.errors / r.count * 100).toFixed(0)}%)</span>`
      : '0';
    tr.innerHTML =
      `<td class="stats-col-count">${r.count}</td>` +
      `<td class="stats-col-errors">${errStr}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.avg)}</td>` +
      `<td class="stats-col-dur">${fmtDur(r.total)}</td>` +
      `<td class="stats-col-method" title="${escapeHTML(r.method)}">${escapeHTML(r.method)}</td>`;
    fragment.appendChild(tr);
  }
  statsTbody.replaceChildren(fragment);
}

function selectStatsRow(r) {
  if (selectedStatsMethod === r.method) {
    selectedStatsMethod = null;
    statsDetailEl.className = '';
    renderStats();
    return;
  }
  selectedStatsMethod = r.method;

  const errStr = r.errors > 0
    ? `${r.errors} (${(r.errors / r.count * 100).toFixed(0)}%)`
    : '0';
  document.getElementById('sd-metrics').innerHTML =
    `<span class="detail-label">Count:</span><span class="detail-value">${r.count}</span>` +
    `<span class="detail-label" style="margin-left:12px">Errors:</span><span class="detail-value">${errStr}</span>` +
    `<span class="detail-label" style="margin-left:12px">Avg:</span><span class="detail-value">${fmtDur(r.avg)}</span>` +
    `<span class="detail-label" style="margin-left:12px">Total:</span><span class="detail-value">${fmtDur(r.total)}</span>`;
  document.getElementById('sd-method').textContent = r.method;
  statsDetailEl.className = 'open';
  renderStats();
}

function copyStatsMethod() {
  if (!selectedStatsMethod) return;
  copyToClipboard(selectedStatsMethod);
}

function selectRow(idx) {
  if (selectedIdx === idx) {
    selectedIdx = -1;
    detailEl.className = '';
    renderTable();
    return;
  }
  selectedIdx = idx;
  const ev = events[idx];
  document.getElementById('d-method').textContent = ev.method;
  document.getElementById('d-time').textContent = fmtTime(ev.start_time);
  document.getElementById('d-dur').textContent = fmtDur(ev.duration_ms);
  document.getElementById('d-protocol').textContent = ev.protocol;
  document.getElementById('d-calltype').textContent = ev.call_type;

  const statusEl = document.getElementById('d-status');
  statusEl.textContent = statusString(ev.status);
  statusEl.className = 'detail-value ' + (ev.status === 0 ? 'status-ok' : 'status-err');

  const errRow = document.getElementById('d-err-row');
  if (ev.error) {
    document.getElementById('d-err').textContent = ev.error;
    errRow.style.display = '';
  } else {
    errRow.style.display = 'none';
  }

  // Headers
  const reqHeaders = ev.request_headers || {};
  const resHeaders = ev.response_headers || {};
  document.getElementById('d-req-headers').textContent = formatHeaders(reqHeaders);
  document.getElementById('d-res-headers').textContent = formatHeaders(resHeaders);
  document.getElementById('d-req-headers-section').style.display = Object.keys(reqHeaders).length > 0 ? '' : 'none';
  document.getElementById('d-res-headers-section').style.display = Object.keys(resHeaders).length > 0 ? '' : 'none';

  // Bodies
  const reqBody = ev.request_body || '';
  const resBody = ev.response_body || '';
  document.getElementById('d-req-body').textContent = reqBody ? decodeBody(reqBody) : '';
  document.getElementById('d-res-body').textContent = resBody ? decodeBody(resBody) : '';
  document.getElementById('d-req-body-section').style.display = reqBody ? '' : 'none';
  document.getElementById('d-res-body-section').style.display = resBody ? '' : 'none';

  // Reset collapsed sections
  document.querySelectorAll('.detail-pre').forEach(el => el.classList.add('collapsed'));
  document.querySelectorAll('.section-chevron').forEach(el => el.textContent = '\u25b8');

  replayOutput.className = '';
  detailEl.className = 'open';
  renderTable();
}

function formatHeaders(headers) {
  const keys = Object.keys(headers).sort();
  return keys.map(k => k + ': ' + headers[k]).join('\n');
}

function toggleSection(name) {
  const el = document.getElementById('d-' + name);
  const chevron = document.getElementById('chevron-' + name);
  if (el.classList.contains('collapsed')) {
    el.classList.remove('collapsed');
    chevron.textContent = '\u25be';
  } else {
    el.classList.add('collapsed');
    chevron.textContent = '\u25b8';
  }
}

// --- Body decoding ---

function decodeBody(b64) {
  try {
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);

    // gRPC frame: 1 byte compressed flag + 4 bytes length + payload
    if (bytes.length >= 5) {
      const frameLen = (bytes[1] << 24) | (bytes[2] << 16) | (bytes[3] << 8) | bytes[4];
      if (frameLen + 5 <= bytes.length) {
        const payload = bytes.slice(5, 5 + frameLen);
        const decoded = decodeProtoWire(payload, '');
        if (decoded) return decoded;
      }
    }

    // Try raw protobuf
    const decoded = decodeProtoWire(bytes, '');
    if (decoded) return decoded;

    // Try UTF-8 text
    const text = new TextDecoder('utf-8', {fatal: true}).decode(bytes);
    // Try JSON pretty-print
    try {
      const obj = JSON.parse(text);
      return JSON.stringify(obj, null, 2);
    } catch (_) {}
    return text;
  } catch (_) {
    // Fallback: hex dump
    try {
      const binary = atob(b64);
      return hexDump(binary);
    } catch (_) {
      return b64;
    }
  }
}

function decodeProtoWire(bytes, indent) {
  if (bytes.length === 0) return null;
  const lines = [];
  let pos = 0;
  while (pos < bytes.length) {
    const tagResult = decodeVarint(bytes, pos);
    if (!tagResult) return null;
    const tag = tagResult.value;
    pos = tagResult.pos;
    const fieldNum = Number(tag >> 3n);
    const wireType = Number(tag & 7n);

    switch (wireType) {
      case 0: { // Varint
        const vr = decodeVarint(bytes, pos);
        if (!vr) return null;
        pos = vr.pos;
        lines.push(indent + fieldNum + ': ' + vr.value.toString());
        break;
      }
      case 1: { // 64-bit
        if (pos + 8 > bytes.length) return null;
        let v = 0n;
        for (let i = 7; i >= 0; i--) v = (v << 8n) | BigInt(bytes[pos + i]);
        pos += 8;
        lines.push(indent + fieldNum + ': ' + v.toString());
        break;
      }
      case 2: { // Length-delimited
        const lr = decodeVarint(bytes, pos);
        if (!lr) return null;
        pos = lr.pos;
        const len = Number(lr.value);
        if (pos + len > bytes.length) return null;
        const data = bytes.slice(pos, pos + len);
        pos += len;
        // Try nested message
        const nested = decodeProtoWire(data, indent + '  ');
        if (nested) {
          lines.push(indent + fieldNum + ': {');
          lines.push(nested);
          lines.push(indent + '}');
        } else if (isUTF8Printable(data)) {
          const str = new TextDecoder().decode(data);
          lines.push(indent + fieldNum + ': "' + str + '"');
        } else {
          lines.push(indent + fieldNum + ': ' + toHex(data));
        }
        break;
      }
      case 5: { // 32-bit
        if (pos + 4 > bytes.length) return null;
        let v = 0;
        for (let i = 3; i >= 0; i--) v = (v << 8) | bytes[pos + i];
        pos += 4;
        lines.push(indent + fieldNum + ': ' + v);
        break;
      }
      default:
        return null;
    }
  }
  return lines.join('\n');
}

function decodeVarint(bytes, pos) {
  let result = 0n;
  let shift = 0n;
  while (pos < bytes.length) {
    const b = bytes[pos];
    pos++;
    result |= BigInt(b & 0x7f) << shift;
    if ((b & 0x80) === 0) return {value: result, pos};
    shift += 7n;
    if (shift > 63n) return null;
  }
  return null;
}

function isUTF8Printable(data) {
  for (let i = 0; i < data.length; i++) {
    if (data[i] < 0x20 && data[i] !== 0x0a && data[i] !== 0x0d && data[i] !== 0x09) return false;
  }
  try {
    new TextDecoder('utf-8', {fatal: true}).decode(data);
    return true;
  } catch (_) {
    return false;
  }
}

function toHex(data) {
  return Array.from(data).map(b => b.toString(16).padStart(2, '0')).join(' ');
}

function hexDump(str) {
  const lines = [];
  for (let i = 0; i < str.length; i += 16) {
    const hex = [];
    let ascii = '';
    for (let j = 0; j < 16; j++) {
      if (i + j < str.length) {
        const c = str.charCodeAt(i + j);
        hex.push(c.toString(16).padStart(2, '0'));
        ascii += (c >= 0x20 && c < 0x7f) ? str[i + j] : '.';
      } else {
        hex.push('  ');
      }
    }
    const addr = i.toString(16).padStart(8, '0');
    lines.push(addr + '  ' + hex.slice(0, 8).join(' ') + '  ' + hex.slice(8).join(' ') + '  |' + ascii + '|');
  }
  return lines.join('\n');
}

// --- Copy ---

function copyBody(which) {
  if (selectedIdx < 0) return;
  const ev = events[selectedIdx];
  const b64 = which === 'request' ? ev.request_body : ev.response_body;
  if (!b64) return;
  const decoded = decodeBody(b64);
  copyToClipboard(decoded);
}

function copyToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => showToast('Copied!')).catch(() => fallbackCopy(text));
  } else {
    fallbackCopy(text);
  }
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  document.body.removeChild(ta);
  showToast('Copied!');
}

// --- Replay ---

async function replayRequest() {
  if (selectedIdx < 0) return;
  const ev = events[selectedIdx];
  const pre = document.getElementById('replay-pre');
  pre.textContent = 'Replaying...';
  pre.className = '';
  replayOutput.className = 'open';

  try {
    const resp = await fetch('/api/replay', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({method: ev.method, request_body: ev.request_body || ''}),
    });
    const data = await resp.json();
    if (data.error) {
      pre.textContent = data.error;
      pre.className = 'replay-error';
    } else if (data.event) {
      const e = data.event;
      let output = `Status: ${statusString(e.status)}\nDuration: ${fmtDur(e.duration_ms)}`;
      if (e.error) output += `\nError: ${e.error}`;
      if (e.response_body) {
        output += '\n\nResponse Body:\n' + decodeBody(e.response_body);
      }
      pre.textContent = output;
      pre.className = '';
    }
  } catch (e) {
    pre.textContent = 'Request failed: ' + e.message;
    pre.className = 'replay-error';
  }
}

// --- Toast ---

function showToast(msg) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.classList.add('show');
  setTimeout(() => t.classList.remove('show'), 2000);
}

// --- Controls ---

function togglePause() {
  paused = !paused;
  const btn = document.getElementById('btn-pause');
  btn.textContent = paused ? 'Resume' : 'Pause';
  btn.classList.toggle('active', paused);
  render();
}

function clearEvents() {
  events.length = 0;
  selectedIdx = -1;
  selectedStatsMethod = null;
  detailEl.className = '';
  statsDetailEl.className = '';
  render();
}

// --- Export ---

function exportData(format) {
  const data = buildExportData();
  const content = format === 'json' ? renderExportJSON(data) : renderExportMarkdown(data);
  const ext = format === 'json' ? 'json' : 'md';
  const now = new Date();
  const ts = now.getFullYear().toString() +
    String(now.getMonth() + 1).padStart(2, '0') +
    String(now.getDate()).padStart(2, '0') + '-' +
    String(now.getHours()).padStart(2, '0') +
    String(now.getMinutes()).padStart(2, '0') +
    String(now.getSeconds()).padStart(2, '0');
  downloadBlob(content, `grpc-tap-${ts}.${ext}`);
}

function buildExportData() {
  const filtered = getFiltered();
  const exported = filtered.map(f => f.ev);

  let periodStart = '';
  let periodEnd = '';
  if (exported.length > 0) {
    periodStart = fmtTimeHMS(exported[0].start_time);
    periodEnd = fmtTimeHMS(exported[exported.length - 1].start_time);
  }

  const calls = exported.map(ev => ({
    time: fmtTime(ev.start_time),
    method: ev.method,
    call_type: ev.call_type,
    protocol: ev.protocol,
    duration_ms: ev.duration_ms,
    status: ev.status,
    error: ev.error || '',
  }));

  return {
    captured: events.length,
    exported: exported.length,
    filter: filterText,
    period: {start: periodStart, end: periodEnd},
    calls,
    analytics: buildExportAnalytics(exported),
  };
}

function fmtTimeHMS(iso) {
  const d = new Date(iso);
  return String(d.getHours()).padStart(2, '0') + ':' +
    String(d.getMinutes()).padStart(2, '0') + ':' +
    String(d.getSeconds()).padStart(2, '0');
}

function buildExportAnalytics(exported) {
  const groups = new Map();
  const order = [];
  for (const ev of exported) {
    const method = ev.method;
    if (!method) continue;
    let g = groups.get(method);
    if (!g) {
      g = {durations: [], errors: 0};
      groups.set(method, g);
      order.push(method);
    }
    g.durations.push(ev.duration_ms);
    if (ev.status !== 0) g.errors++;
  }
  return order.map(method => {
    const g = groups.get(method);
    const durs = g.durations.slice().sort((a, b) => a - b);
    const count = durs.length;
    const total = durs.reduce((s, d) => s + d, 0);
    const avg = total / count;
    const p95 = durs[Math.floor((count - 1) * 0.95)];
    const mx = durs[count - 1];
    return {method, count, errors: g.errors, total_ms: total, avg_ms: avg, p95_ms: p95, max_ms: mx};
  });
}

function fmtDurExport(ms) {
  if (ms < 1) return Math.round(ms * 1000) + '\u00b5s';
  if (ms < 1000) return ms.toFixed(1) + 'ms';
  return (ms / 1000).toFixed(2) + 's';
}

function renderExportJSON(data) {
  return JSON.stringify(data, null, '  ') + '\n';
}

function renderExportMarkdown(data) {
  let md = '# grpc-tap export\n\n';
  md += `- Captured: ${data.captured} calls\n`;
  let exportLine = `- Exported: ${data.exported} calls`;
  if (data.filter) {
    exportLine += ` (filter: ${escPipe(data.filter)})`;
  }
  md += exportLine + '\n';
  if (data.period.start) {
    md += `- Period: ${data.period.start} \u2014 ${data.period.end}\n`;
  }

  md += '\n## Calls\n\n';
  md += '| # | Time | Method | Type | Protocol | Duration | Status | Error |\n';
  md += '|---|------|--------|------|----------|----------|--------|-------|\n';
  data.calls.forEach((c, i) => {
    md += `| ${i + 1} | ${c.time} | ${escPipe(c.method)} | ${c.call_type} | ${c.protocol} | ${fmtDurExport(c.duration_ms)} | ${statusString(c.status)} | ${escPipe(c.error)} |\n`;
  });

  if (data.analytics.length > 0) {
    md += '\n## Analytics\n\n';
    md += '| Method | Count | Errors | Avg | P95 | Max | Total |\n';
    md += '|--------|-------|--------|-----|-----|-----|-------|\n';
    for (const a of data.analytics) {
      const errStr = a.errors > 0 ? `${a.errors}(${(a.errors / a.count * 100).toFixed(0)}%)` : '0';
      md += `| ${escPipe(a.method)} | ${a.count} | ${errStr} | ${fmtDurExport(a.avg_ms)} | ${fmtDurExport(a.p95_ms)} | ${fmtDurExport(a.max_ms)} | ${fmtDurExport(a.total_ms)} |\n`;
    }
  }

  return md;
}

function escPipe(s) {
  return (s || '').replace(/\|/g, '\\|');
}

function downloadBlob(content, filename) {
  const blob = new Blob([content], {type: 'text/plain'});
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// --- SSE ---

function connectSSE() {
  const es = new EventSource('/api/events');
  es.onopen = () => {
    statusEl.textContent = 'connected';
    statusEl.className = 'status connected';
  };
  es.onmessage = (e) => {
    if (paused) return;
    const ev = JSON.parse(e.data);
    events.push(ev);
    render();
  };
  es.onerror = () => {
    statusEl.textContent = 'disconnected';
    statusEl.className = 'status disconnected';
    es.close();
    setTimeout(connectSSE, 2000);
  };
}

connectSSE();
