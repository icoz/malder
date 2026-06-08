document.addEventListener('DOMContentLoaded', function () {
  var form = document.getElementById('research-form');
  if (!form) return;

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var query = document.getElementById('query').value.trim();
    if (!query) return;

    var submitBtn = document.getElementById('submit-btn');
    var progress = document.getElementById('progress');
    var progressFill = document.getElementById('progress-fill');
    var progressStatus = document.getElementById('progress-status');
    var eventLog = document.getElementById('event-log');
    var errorDiv = document.getElementById('error');

    errorDiv.style.display = 'none';
    eventLog.innerHTML = '';
    progress.style.display = 'block';
    progressFill.style.width = '0%';
    progressStatus.textContent = 'Подключаемся...';
    submitBtn.disabled = true;

    var evtSource = new EventSource('/api/research/stream?q=' + encodeURIComponent(query));
    var reportId = null;
    var stageTimeout = setTimeout(function () {
      evtSource.close();
      showError('Сервер не отвечает. Попробуйте ещё раз.', submitBtn, progress, errorDiv);
    }, 120000);

    function addLog(text) {
      var item = document.createElement('div');
      item.className = 'event-log-item';
      item.innerHTML = '<span class="event-log-dot"></span>' + escapeHtml(text);
      eventLog.appendChild(item);
      eventLog.scrollTop = eventLog.scrollHeight;
    }

    function setProgress(pct, label) {
      progressFill.style.width = Math.min(pct, 100) + '%';
      if (label) progressStatus.textContent = label;
    }

    evtSource.addEventListener('started', function (event) {
      var data = JSON.parse(event.data);
      reportId = data.report_id;
      setProgress(5, 'Планируем исследование...');
    });

    evtSource.addEventListener('planning', function () { setProgress(5, 'Составляем план...'); });
    evtSource.addEventListener('plan_complete', function () { addLog('План исследования составлен'); setProgress(10, 'План готов'); });
    evtSource.addEventListener('search_start', function () { addLog('Поиск в интернете...'); setProgress(15, 'Выполняем поиск...'); });
    evtSource.addEventListener('search_complete', function () { addLog('Поиск завершён'); setProgress(30, 'Поиск завершён'); });
    evtSource.addEventListener('subtopic_analysis_start', function () { addLog('Анализ подтем...'); setProgress(35, 'Анализируем подтемы...'); });
    evtSource.addEventListener('subtopic_analysis_complete', function (event) { var d = JSON.parse(event.data); addLog('Проанализировано подтем: ' + d.completed); setProgress(55, 'Анализ подтем завершён'); });
    evtSource.addEventListener('section_synthesis_start', function () { addLog('Синтез разделов...'); setProgress(60, 'Синтезируем разделы...'); });
    evtSource.addEventListener('section_synthesis_complete', function (event) { var d = JSON.parse(event.data); addLog('Синтезировано разделов: ' + d.sections); setProgress(75, 'Синтез разделов завершён'); });
    evtSource.addEventListener('critic_loop_start', function () { addLog('Проверка качества...'); setProgress(80, 'Проверяем отчёт...'); });
    evtSource.addEventListener('synthesis_start', function () { addLog('Формирование финального отчёта...'); setProgress(85, 'Формируем итоговый отчёт...'); });
    evtSource.addEventListener('synthesis_complete', function () { setProgress(90, 'Отчёт сформирован'); });
    evtSource.addEventListener('exec_summary_start', function () { addLog('Подготовка резюме...'); setProgress(92, 'Готовим краткое резюме...'); });
    evtSource.addEventListener('exec_summary_complete', function () { addLog('Резюме готово'); setProgress(95, 'Резюме готово'); });
    evtSource.addEventListener('critic_start', function () { addLog('Оценка критика...'); setProgress(82, 'Критик оценивает...'); });
    evtSource.addEventListener('critic_complete', function (event) { var d = JSON.parse(event.data); var s = 'Оценка: ' + d.score + '/10'; if (d.weak_sections && d.weak_sections.length) s += ', слабые разделы: ' + d.weak_sections.join(', '); addLog(s); setProgress(84, 'Оценка получена'); });
    evtSource.addEventListener('additional_search_start', function () { addLog('Дополнительный поиск...'); setProgress(70, 'Дополнительный поиск...'); });
    evtSource.addEventListener('additional_search_complete', function () { addLog('Дополнительный поиск завершён'); setProgress(78, 'Дополнительный поиск завершён'); });
    evtSource.addEventListener('section_deepen_start', function () { addLog('Углубление слабых разделов...'); setProgress(76, 'Дорабатываем разделы...'); });
    evtSource.addEventListener('section_deepen_complete', function () { addLog('Разделы доработаны'); setProgress(79, 'Разделы доработаны'); });
    evtSource.addEventListener('finish', function () { setProgress(100, 'Исследование завершено!'); });

    evtSource.addEventListener('result', function (event) {
      clearTimeout(stageTimeout);
      evtSource.close();
      setProgress(100, 'Исследование завершено!');
      var data = JSON.parse(event.data);
      setTimeout(function () { window.location.href = '/reports/' + data.report_id; }, 600);
    });

    evtSource.addEventListener('error', function (event) {
      clearTimeout(stageTimeout);
      evtSource.close();
      showError(event.data || 'Произошла ошибка при исследовании.', submitBtn, progress, errorDiv);
    });

    evtSource.addEventListener('cancelled', function () {
      clearTimeout(stageTimeout);
      evtSource.close();
      showError('Исследование отменено.', submitBtn, progress, errorDiv);
    });

    // Reset timeout on any data
    evtSource.onmessage = function () {
      clearTimeout(stageTimeout);
      stageTimeout = setTimeout(function () {
        evtSource.close();
        showError('Сервер не отвечает. Попробуйте ещё раз.', submitBtn, progress, errorDiv);
      }, 120000);
    };
  });

  // Auto-refresh for in-progress reports on detail page
  var progressDiv = document.getElementById('progress');
  if (progressDiv && progressDiv.dataset.reportId) {
    var reportId = progressDiv.dataset.reportId;
    var pollInterval = setInterval(function () {
      fetch('/api/reports/' + reportId)
        .then(function (r) { return r.json(); })
        .then(function (report) {
          if (report.status !== 'in_progress') {
            clearInterval(pollInterval);
            location.reload();
          }
        })
        .catch(function () {});
    }, 3000);
  }

  // Nav: highlight active link
  var path = window.location.pathname;
  document.querySelectorAll('.nav-links a').forEach(function (a) {
    if (a.getAttribute('href') === path) a.classList.add('active');
  });

  function showError(msg, btn, progressEl, errorEl) {
    if (progressEl) progressEl.style.display = 'none';
    if (errorEl) { errorEl.style.display = 'block'; errorEl.textContent = msg; }
    if (btn) btn.disabled = false;
  }

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }
});