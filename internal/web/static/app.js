/* Дашборд сайта: тянет JSON из /api/sites/{id}/... и рисует таблицы и SVG-график. */
(function () {
  var root = document.getElementById('dash'); if (!root) return;
  var site = root.getAttribute('data-site');
  var $ = function (id) { return document.getElementById(id); };
  var state = { preset: '7d', from: '', to: '', tab: 'path' };
  try { var saved = JSON.parse(localStorage.getItem('lupa.period') || '{}'); if (saved.preset) state = Object.assign(state, saved); } catch (e) {}

  function q(path, params) {
    var u = '/api/sites/' + site + '/' + path + '?preset=' + state.preset + '&from=' + state.from + '&to=' + state.to + (params || '');
    return fetch(u, { credentials: 'same-origin' }).then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); });
  }
  function fmt(n) { return (+n || 0).toLocaleString('ru-RU'); }
  function pct(x) { return (Math.round((+x || 0) * 1000) / 10).toLocaleString('ru-RU') + ' %'; }
  function dur(s) { s = Math.round(+s || 0); return s < 60 ? s + ' с' : Math.floor(s / 60) + ' мин ' + (s % 60) + ' с'; }
  function delta(cur, prev, invert) {
    if (!prev) return '';
    var d = (cur - prev) / prev; if (!isFinite(d)) return '';
    var up = d >= 0; var good = invert ? !up : up;
    return '<i class="' + (good ? 'up' : 'down') + '">' + (up ? '+' : '') + Math.round(d * 100) + ' %</i>';
  }
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }

  function table(el, rows, keyLabel, cols) {
    if (!rows || !rows.length) { el.innerHTML = '<div class="empty">Нет данных за период</div>'; return; }
    var max = Math.max.apply(null, rows.map(function (r) { return +r[cols[0].f] || 0; })) || 1;
    var h = '<table><thead><tr><th>' + keyLabel + '</th>' + cols.map(function (c) { return '<th class="n">' + c.l + '</th>'; }).join('') + '</tr></thead><tbody>';
    rows.forEach(function (r) {
      var w = Math.round((+r[cols[0].f] || 0) / max * 100);
      h += '<tr class="row"><td class="k" title="' + esc(r.key) + '"><span class="fill" style="width:' + w + '%"></span>' + esc(r.key || '(пусто)') + '</td>' +
        cols.map(function (c) { return '<td class="n">' + (c.fmt ? c.fmt(r[c.f]) : fmt(r[c.f])) + '</td>'; }).join('') + '</tr>';
    });
    el.innerHTML = h + '</tbody></table>';
  }

  // Простой SVG-график: линии посетителей и просмотров.
  function chart(el, points, unit) {
    if (!points || !points.length) { el.innerHTML = '<div class="empty">Нет данных за период</div>'; return; }
    var W = el.clientWidth || 800, H = 240, pl = 44, pr = 12, pt = 12, pb = 28;
    var maxV = Math.max.apply(null, points.map(function (p) { return Math.max(+p.visitors, +p.pageviews); })) || 1;
    var n = points.length, x = function (i) { return pl + (n === 1 ? (W - pl - pr) / 2 : i * (W - pl - pr) / (n - 1)); }, y = function (v) { return pt + (H - pt - pb) * (1 - v / maxV); };
    function line(f, color, width) { return '<polyline fill="none" stroke="' + color + '" stroke-width="' + width + '" points="' + points.map(function (p, i) { return x(i) + ',' + y(+p[f]); }).join(' ') + '"/>'; }
    var grid = '';
    for (var g = 0; g <= 4; g++) { var v = maxV * g / 4, yy = y(v); grid += '<line x1="' + pl + '" x2="' + (W - pr) + '" y1="' + yy + '" y2="' + yy + '" stroke="var(--line)"/><text x="' + (pl - 6) + '" y="' + (yy + 4) + '" text-anchor="end">' + Math.round(v) + '</text>'; }
    var labels = '', step = Math.max(1, Math.ceil(n / 8));
    points.forEach(function (p, i) { if (i % step === 0 || i === n - 1) { var t = p.t; var lab = unit === 'hour' ? t.slice(11, 16) : t.slice(8, 10) + '.' + t.slice(5, 7); labels += '<text x="' + x(i) + '" y="' + (H - 8) + '" text-anchor="middle">' + lab + '</text>'; } });
    var dots = points.map(function (p, i) { return '<circle cx="' + x(i) + '" cy="' + y(+p.visitors) + '" r="2.5" fill="var(--accent)"><title>' + p.t + ': ' + p.visitors + ' посетителей, ' + p.pageviews + ' просмотров</title></circle>'; }).join('');
    el.innerHTML = '<svg class="chart" viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="none">' + grid + line('pageviews', 'var(--line)', 2) + line('visitors', 'var(--accent)', 2.5) + dots + labels + '</svg>' +
      '<div class="small muted">Синяя линия: посетители. Серая: просмотры.</div>';
  }

  function loadAll() {
    try { localStorage.setItem('lupa.period', JSON.stringify(state)); } catch (e) {}
    q('overview').then(function (d) {
      var c = d.current, p = d.previous;
      $('kpis').innerHTML =
        '<div class="kpi"><b>' + fmt(c.visitors) + delta(c.visitors, p.visitors) + '</b><span>Посетители</span></div>' +
        '<div class="kpi"><b>' + fmt(c.visits) + delta(c.visits, p.visits) + '</b><span>Визиты</span></div>' +
        '<div class="kpi"><b>' + fmt(c.pageviews) + delta(c.pageviews, p.pageviews) + '</b><span>Просмотры</span></div>' +
        '<div class="kpi"><b>' + pct(c.bounce_rate) + delta(c.bounce_rate, p.bounce_rate, true) + '</b><span>Отказы</span></div>' +
        '<div class="kpi"><b>' + dur(c.avg_duration) + delta(c.avg_duration, p.avg_duration) + '</b><span>Время на сайте</span></div>' +
        '<div class="kpi"><b>' + fmt(c.events) + delta(c.events, p.events) + '</b><span>События</span></div>';
    }).catch(function () { $('kpis').innerHTML = '<div class="err">Не удалось загрузить сводку</div>'; });
    q('timeseries').then(function (d) { chart($('chart'), d.points, d.unit); });
    loadTab();
    q('breakdown', '&by=source&limit=12').then(function (d) { table($('sources'), d.rows, 'Источник', [{ f: 'visitors', l: 'Посетители' }, { f: 'visits', l: 'Визиты' }]); });
    q('breakdown', '&by=utm_campaign&limit=12').then(function (d) { table($('campaigns'), d.rows, 'UTM-кампания', [{ f: 'visitors', l: 'Посетители' }, { f: 'visits', l: 'Визиты' }]); });
    q('breakdown', '&by=device&limit=5').then(function (d) { table($('devices'), d.rows, 'Устройство', [{ f: 'visitors', l: 'Посетители' }]); });
    q('breakdown', '&by=browser&limit=8').then(function (d) { table($('browsers'), d.rows, 'Браузер', [{ f: 'visitors', l: 'Посетители' }]); });
    q('breakdown', '&by=os&limit=8').then(function (d) { table($('oses'), d.rows, 'ОС', [{ f: 'visitors', l: 'Посетители' }]); });
    q('goals').then(function (d) {
      var el = $('goals');
      var rows = (d.goals || []).map(function (g) { return { key: g.name, count: g.count, sessions: g.sessions, conversion: g.conversion }; });
      if (!rows.length) { el.innerHTML = '<div class="empty">Целей нет. Добавьте в настройках сайта.</div>'; return; }
      table(el, rows, 'Цель', [{ f: 'sessions', l: 'Визитов с целью' }, { f: 'count', l: 'Срабатываний' }, { f: 'conversion', l: 'Конверсия', fmt: pct }]);
    });
    q('events').then(function (d) {
      var rows = (d.events || []).map(function (e) { return { key: e.name, count: e.count, sessions: e.sessions, conversion: e.conversion }; });
      table($('events'), rows, 'Событие', [{ f: 'count', l: 'Всего' }, { f: 'sessions', l: 'Визитов' }, { f: 'conversion', l: 'Доля визитов', fmt: pct }]);
    });
  }
  function loadTab() {
    var by = state.tab;
    document.querySelectorAll('#pagetabs button').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-by') === by); });
    var p = by === 'entry' ? q('entry', '&limit=25') : q('breakdown', '&by=' + by + '&limit=25');
    p.then(function (d) { table($('pages'), d.rows, by === 'title' ? 'Заголовок' : 'Страница', by === 'entry' ? [{ f: 'visits', l: 'Входов' }, { f: 'visitors', l: 'Посетители' }] : [{ f: 'visitors', l: 'Посетители' }, { f: 'pageviews', l: 'Просмотры' }]); });
  }
  function realtime() {
    fetch('/api/sites/' + site + '/realtime', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      $('rt-count').textContent = fmt(d.visitors);
      table($('rt-pages'), d.pages, 'Сейчас на странице', [{ f: 'visitors', l: 'Посетители' }]);
      chart($('rt-chart'), d.minutes.map(function (m) { return { t: '           ' + m.t, visitors: m.visitors, pageviews: m.pageviews }; }), 'hour');
    }).catch(function () {});
  }

  // управление периодом
  var sel = $('preset'); sel.value = state.preset;
  $('from').value = state.from; $('to').value = state.to;
  function custom() { $('customrange').style.display = sel.value === 'custom' ? '' : 'none'; }
  sel.addEventListener('change', function () { state.preset = sel.value; custom(); if (sel.value !== 'custom') loadAll(); });
  $('apply').addEventListener('click', function () { state.from = $('from').value; state.to = $('to').value; if (state.from && state.to) loadAll(); });
  custom();
  document.querySelectorAll('#pagetabs button').forEach(function (b) { b.addEventListener('click', function () { state.tab = b.getAttribute('data-by'); loadTab(); }); });

  // воронка
  $('funnel-run').addEventListener('click', function () {
    var steps = $('funnel-steps').value.split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
    q('funnel', '&steps=' + encodeURIComponent(steps.join('|'))).then(function (d) {
      var el = $('funnel-out');
      if (d.error) { el.innerHTML = '<div class="err">' + esc(d.error) + '</div>'; return; }
      el.innerHTML = d.steps.map(function (s, i) {
        var drop = i > 0 && d.steps[i - 1].sessions ? ' <span class="muted small">(−' + Math.round((1 - s.sessions / d.steps[i - 1].sessions) * 100) + ' %)</span>' : '';
        return '<div class="step"><span class="mono">' + esc(s.step) + '</span><b>' + fmt(s.sessions) + '</b><span>' + pct(s.rate) + drop + '</span></div>';
      }).join('');
    }).catch(function () { $('funnel-out').innerHTML = '<div class="err">Ошибка запроса</div>'; });
  });

  loadAll(); realtime(); setInterval(realtime, 10000);
  window.addEventListener('resize', function () { q('timeseries').then(function (d) { chart($('chart'), d.points, d.unit); }); });
})();
