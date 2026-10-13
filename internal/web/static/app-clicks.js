(function () {
  var root = document.getElementById('clicks'); if (!root) return;
  var site = root.getAttribute('data-site'), $ = function (id) { return document.getElementById(id); };
  var fmt = function (n) { return (+n || 0).toLocaleString('ru-RU'); }, pct = function (x) { return Math.round((+x || 0) * 100) + ' %'; };
  var esc = function (s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); };
  function load() {
    var path = $('path').value || '';
    fetch('/api/sites/' + site + '/clicks?preset=' + $('preset').value + '&path=' + encodeURIComponent(path), { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (d) {
      var sel = $('path'), cur = d.path;
      sel.innerHTML = (d.pages.length ? d.pages : [{ key: cur, pageviews: 0 }]).map(function (p) { return '<option value="' + esc(p.key) + '"' + (p.key === cur ? ' selected' : '') + '>' + esc(p.key) + ' · ' + fmt(p.pageviews) + ' кликов</option>'; }).join('');
      $('kpis').innerHTML = '<div class="kpi"><b>' + fmt(d.pageviews) + '</b><span>Просмотров страницы</span></div><div class="kpi"><b>' + fmt(d.clicks) + '</b><span>Кликов</span></div>';
      if (!d.elements.length) { $('elements').innerHTML = '<div class="empty">Кликов за период нет. Проверьте, что у счётчика есть data-clicks.</div>'; }
      else {
        var max = d.elements[0].clicks || 1;
        $('elements').innerHTML = '<table><thead><tr><th>Элемент</th><th class="n">Кликов</th><th class="n">Посетителей</th><th class="n">Доля</th></tr></thead><tbody>' + d.elements.map(function (e) {
          return '<tr class="row"><td class="k" title="' + esc(e.selector) + '"><span class="fill" style="width:' + Math.round(e.clicks / max * 100) + '%"></span><b>' + esc(e.text || '(без текста)') + '</b><br><span class="mono muted" style="font-size:11px">' + esc(e.selector) + '</span></td><td class="n">' + fmt(e.clicks) + '</td><td class="n">' + fmt(e.visitors) + '</td><td class="n">' + pct(e.share) + '</td></tr>';
        }).join('') + '</tbody></table>';
      }
      $('scroll').innerHTML = (d.scroll.length ? d.scroll : [{ depth: 25, reached: 0 }, { depth: 50, reached: 0 }, { depth: 75, reached: 0 }, { depth: 100, reached: 0 }]).map(function (s) { return '<div><span>' + s.depth + ' %</span><i style="--w:' + Math.round(s.reached * 100) + '%"></i><span class="n">' + pct(s.reached) + '</span></div>'; }).join('');
    }).catch(function () { $('elements').innerHTML = '<div class="err">Ошибка загрузки</div>'; });
  }
  $('preset').addEventListener('change', load);
  $('path').addEventListener('change', load);
  $('open').addEventListener('click', function () {
    fetch('/api/sites/' + site + '/heatmap-url?path=' + encodeURIComponent($('path').value || '/') + '&preset=' + $('preset').value, { credentials: 'same-origin' })
      .then(function (r) { return r.json(); }).then(function (d) { $('hint').textContent = 'Открываю ' + d.url.split('#')[0]; window.open(d.url, '_blank'); });
  });
  load();
})();
