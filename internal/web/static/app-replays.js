(function () {
  var fmt = function (n) { return (+n || 0).toLocaleString('ru-RU'); };
  var esc = function (s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); };
  var dur = function (s) { s = +s || 0; return s < 60 ? s + ' с' : Math.floor(s / 60) + ' мин ' + (s % 60) + ' с'; };
  var list = document.getElementById('replays');
  if (list) {
    var site = list.getAttribute('data-site'), $ = function (id) { return document.getElementById(id); };
    function load() {
      fetch('/api/sites/' + site + '/replays?preset=' + $('preset').value, { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
        $('count').textContent = d.replays.length ? d.replays.length + ' записей' : '';
        if (!d.replays.length) { $('list').innerHTML = '<div class="empty">Записей за период нет.</div>'; return; }
        $('list').innerHTML = '<table><thead><tr><th>Начало</th><th>Первая страница</th><th class="n">Длительность</th><th class="n">Страниц</th><th>Устройство</th><th>Источник</th><th class="n">Размер</th></tr></thead><tbody>' + d.replays.map(function (r) {
          return '<tr><td><a href="/sites/' + site + '/replays/' + encodeURIComponent(r.rid) + '">' + esc(r.start) + '</a></td><td class="mono">' + esc(r.first_path) + '</td><td class="n">' + dur(r.duration) + '</td><td class="n">' + fmt(r.pages) + '</td><td>' + esc(r.device + ' · ' + r.browser + ' · ' + r.os) + '</td><td>' + esc(r.source) + '</td><td class="n">' + Math.round(r.bytes / 1024) + ' КБ</td></tr>';
        }).join('') + '</tbody></table>';
      }).catch(function () { $('list').innerHTML = '<div class="err">Ошибка загрузки</div>'; });
    }
    $('preset').addEventListener('change', load);
    load();
  }
  var one = document.getElementById('replay');
  if (one && window.rrwebPlayer) {
    var site = one.getAttribute('data-site'), rid = one.getAttribute('data-rid'), box = document.getElementById('player');
    fetch('/api/sites/' + site + '/replays/' + encodeURIComponent(rid), { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (events) {
      if (!events || events.length < 2) { box.innerHTML = '<div class="empty">В записи слишком мало событий.</div>'; return; }
      new rrwebPlayer({ target: box, props: { events: events, width: Math.min(box.clientWidth - 20, 1100), height: 600, autoPlay: false, skipInactive: true, showController: true } });
    }).catch(function () { box.innerHTML = '<div class="err">Не удалось загрузить запись</div>'; });
  }
})();
