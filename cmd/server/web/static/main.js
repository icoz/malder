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
    var resultDiv = document.getElementById('result');
    var resultMeta = document.getElementById('report-meta');
    var resultContent = document.getElementById('report-content');
    var errorDiv = document.getElementById('error');

    resultDiv.style.display = 'none';
    errorDiv.style.display = 'none';
    progress.style.display = 'block';
    progressFill.style.width = '0%';
    progressStatus.textContent = 'Подключаемся...';
    submitBtn.disabled = true;

    var evtSource = new EventSource('/api/research/stream?q=' + encodeURIComponent(query));
    var reportId = null;

    evtSource.addEventListener('started', function (event) {
      var data = JSON.parse(event.data);
      reportId = data.report_id;
      progressStatus.textContent = 'Планируем исследование...';
    });

    evtSource.addEventListener('search_start', function () {
      progressFill.style.width = '15%';
      progressStatus.textContent = 'Выполняем поиск в интернете...';
    });

    evtSource.addEventListener('search_complete', function () {
      progressFill.style.width = '30%';
      progressStatus.textContent = 'Поиск завершён, приступаем к анализу...';
    });

    evtSource.addEventListener('subtopic_analysis_start', function () {
      progressFill.style.width = '35%';
      progressStatus.textContent = 'Анализируем подтемы...';
    });

    evtSource.addEventListener('subtopic_analysis_complete', function (event) {
      progressFill.style.width = '55%';
      var data = JSON.parse(event.data);
      progressStatus.textContent = 'Проанализировано подтем: ' + data.completed;
    });

    evtSource.addEventListener('section_synthesis_start', function () {
      progressFill.style.width = '60%';
      progressStatus.textContent = 'Синтезируем разделы...';
    });

    evtSource.addEventListener('section_synthesis_complete', function (event) {
      progressFill.style.width = '75%';
      var data = JSON.parse(event.data);
      progressStatus.textContent = 'Синтезировано разделов: ' + data.sections;
    });

    evtSource.addEventListener('critic_loop_start', function () {
      progressFill.style.width = '80%';
      progressStatus.textContent = 'Проверяем и дорабатываем отчёт...';
    });

    evtSource.addEventListener('synthesis_start', function () {
      progressFill.style.width = '85%';
      progressStatus.textContent = 'Формируем итоговый отчёт...';
    });

    evtSource.addEventListener('synthesis_complete', function () {
      progressFill.style.width = '90%';
    });

    evtSource.addEventListener('critic_start', function () {
      progressStatus.textContent = 'Критик оценивает отчёт...';
    });

    evtSource.addEventListener('additional_search_start', function () {
      progressFill.style.width = '70%';
      progressStatus.textContent = 'Выполняем дополнительный поиск...';
    });

    evtSource.addEventListener('result', function (event) {
      evtSource.close();
      var data = JSON.parse(event.data);
      var url = '/reports/' + data.report_id;
      window.location.href = url;
    });

    evtSource.addEventListener('error', function (event) {
      evtSource.close();
      progress.style.display = 'none';
      errorDiv.style.display = 'block';
      errorDiv.textContent = event.data || 'Произошла ошибка при исследовании.';
      submitBtn.disabled = false;
    });

    evtSource.addEventListener('cancelled', function () {
      evtSource.close();
      progress.style.display = 'none';
      errorDiv.style.display = 'block';
      errorDiv.textContent = 'Исследование отменено.';
      submitBtn.disabled = false;
    });
  });

  // Auto-refresh for in-progress reports
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
});
