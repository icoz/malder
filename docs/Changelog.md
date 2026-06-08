# Changelog

## [Unreleased]

### Добавлено

#### Логгирование LLM с req_id

- Каждый вызов `Complete()` получает `req_id` (12-символьный hex из `time.Now().UnixNano()`)
- Entry DEBUG при старте: model, chars, req_id
- INFO при успешном ответе: req_id, duration, chars
- WARN на каждой неудачной попытке: req_id, status, attempt, reason
- **Финальный WARN** при исчерпании retry: model, chars, attempts, duration, last_err, http_status, body_snippet
- **Timeout vs cancel**: `context.DeadlineExceeded` → `"timeout after %s"`, `context.Canceled` → `"client disconnected"`

#### logPageHandler / logResponseWriter

- Все HTTP-хендлеры обёрнуты в `logPageHandler`, логирующий при выходе: `← WebUI.handlerName: GET /path → 200 (1.2s)`
- Inline DEBUG логи из хендлеров удалены (заменены единым wrapper)

#### ProgressSaver callback

- В `CoordinatorAgent` добавлен тип `ProgressSaver func(reportID, sectionID string, event string, data map[string]any)`
- Вызывается внутри `c.report()` на каждом этапе — сохраняет прогресс в `ReportStore.SaveProgress()`
- Позволяет восстановить состояние на странице деталей при перезагрузке

#### In-place progress polling

- Страница деталей отчёта: in-place polling `/api/reports/{id}` раз в 3с
- Обновление progress-секции через DOM (фаза, счётчики, critic score/iteration)
- Полный reload только при финальном статусе (`completed`/`error`)

#### DEBUG-логи с размерами промптов в координаторе

- `createPlan`: DEBUG с количеством queries, sections, subtopics
- `synthesizeSection`: DEBUG с именем секции
- `synthesizeFinal`: DEBUG с iteration + feedback length
- Critic `Evaluate`: DEBUG с длиной отчёта
- `extractQueriesFromFeedback`: DEBUG с числом запросов
- `generateExecutiveSummary`: DEBUG с длиной отчёта

### Исправлено

#### Template namespace conflict (critical)

- `ParseFS(templateFS, "web/templates/*.html")` — все `{{define "content"}}` в общем namespace, алфавитно последний файл (`report_list.html`) перезаписывал все остальные
- Фикс: каждый хендлер создаёт свой `*template.Template` через `base.Clone()` + парсинг собственного файла
- Удалён подход с единым `ParseFS(..., "web/templates/*.html")`

#### pre-existing bug: synthesizeReport fmt.Sprintf

- В `synthesizeReport` было 4 `%s` placeholder-а, но передавалось 5 аргументов — `lengthGuide` и `sourcesText` не вставлялись в промпт
- Добавлены недостающие `%s` слоты

#### JS event-data поля

- SSE-события переименованы: `d.completed` / `d.total` вместо `d.sections` (для единообразия с critic-данными)

### Файлы, затронутые в этой версии

| Файл | Изменения |
|------|-----------|
| `internal/llm/client.go` | req_id во всех Complete() логах; финальный WARN с контекстом; timeout vs cancel; retry-log теперь с req_id, model, chars |
| `internal/memory/longterm.go` | RetryConfig, retryWithBackoff() для Save() и RecallWithTopK() |
| `internal/memory/report.go` | RawProgress поле, SaveProgress(), FailInProgressReports() |
| `internal/agent/coordinator.go` | ProgressSaver тип/поле/сеттер; enhanced report() с подтемами/итерациями/counters; DEBUG лог размеров промптов; фикс fmt.Sprintf |
| `cmd/server/main.go` | logResponseWriter + logPageHandler; per-page template через base.Clone(); progress saver wiring; все WS хендлеры обёрнуты в logPageHandler; inline DEBUG логи удалены |
| `cmd/server/web/templates/report_detail.html` | progress-секция для in_progress (план, фаза, counters, critic) |
| `cmd/server/web/static/main.js` | in-place polling; SSE event fields d.completed/d.total |
| `docs/Architech.md` | ProgressSaver, in-place polling, logPageHandler, retry detail |

---

## [Unreleased] (предыдущие)

### Добавлено

#### Управление детализацией отчётов (VerbosityLevel)

Добавлен трёхуровневый контроль детализации исследования, управляемый через переменную окружения `VERBOSITY` (`brief` / `normal` / `detailed`). По умолчанию — `normal`.

- **Масштаб плана**: `brief` → 1-2 секции × 1-2 подтемы; `detailed` → 4-6 секций × 3-5 подтем
- **Длина анализа**: каждый агент получает указание целевого объёма в словах
- **Количество фактов**: суммаризация страниц — 3-5 / 3-7 / 7-15 фактов
- **Лимиты на сырые данные**: длина raw (5K / 10K / 20K) и summary (1.5K / 3K / 6K)
- **MaxTokens**: ограничение ответа LLM (512-2048 / 2048-8192 / 4096-16384)

#### MaxTokens в LLM-клиенте

В `internal/llm/client.go`:
- Добавлено поле `MaxTokens *int` в `chatRequest` — передаётся как `max_tokens` в API
- Методы `Complete` и `CompleteSimple` теперь принимают параметр `maxTokens int`
- Значение 0 означает «без ограничения» (поле не отправляется)

#### Итеративное углубление в criticLoop

- Критик теперь возвращает `weak_sections` — имена разделов, требующих доработки
- Обратная связь критика передаётся в промпт следующей итерации `synthesizeFinal`
- Дополнительный поиск по gap-запросам сохранён и дополнен передачей контекста

#### Executive Summary (только detailed)

- При `VERBOSITY=detailed` после финального отчёта генерируется краткое резюме (200-400 слов)
- Содержит: основной вопрос, 3-5 ключевых выводов, рекомендации
- Сохраняется в `Report.ExecutiveSummary` и отображается в веб-интерфейсе

#### Веб-интерфейс (v2)

**Исправления:**
- **Critical**: контент отчёта не отображался — `$r.ReportHTML` заменён на `$.ReportHTML` в `report_detail.html` (поле `ReportHTML` не входит в структуру `Report`, передаётся отдельным ключом шаблона)

**CSS — полный редизайн:**
- CSS custom properties для единой цветовой схемы
- Тёмная тема (automatic через `prefers-color-scheme: dark`)
- Sticky-навбар с backdrop-filter blur
- Карточная вёрстка с тенями и бордерами
- Типографика: заголовки, код, блокцитаты, таблицы, изображения
- Адаптивность для мобильных (столбцы вместо строк на малых экранах)
- Статус-бейджи с CSS-индикаторами и pulse-анимацией для in_progress
- Style print: скрытие навбара, убирание теней
- Копи-нотификация (toast-уведомление)

**Новые страницы:**
- `404.html` — стилизованная страница «не найдено» с кнопкой на главную
- `error.html` — страница ошибки сервера
- Catch-all route `GET /{path...}` для неизвестных путей

**Улучшения детальной страницы отчёта:**
- Оглавление (TOC): автоматически генерируется из h2-заголовков отчёта, якорные ссылки
- Кнопка «Копировать» — fetch raw markdown → clipboard (с fallback через textarea)
- Счётчик слов в мета-блоке
- Блок «Краткое резюме» с акцентной рамкой (если есть)

**Улучшения страницы исследования:**
- Event-log: хронология этапов исследования под прогресс-баром
- SSE timeout: 120 секунд без данных → сообщение об ошибке
- Более точный прогресс-бар по этапам (0-100%)
- Детальная информация этапов в логе (оценка критика, количество подтем/разделов)
- Выделение активной ссылки в навбаре

**JavaScript:**
- Реструктуризация: общий код (TOC, copy, nav) выполняется на всех страницах, код формы — только на index
- Генерация TOC из h2 заголовков
- Копирование с Clipboard API + execCommand fallback
- Сброс SSE timeout при любом событии

### Файлы, затронутые в этой версии

| Файл | Изменения |
|------|-----------|
| `internal/llm/client.go` | MaxTokens в chatRequest, новый параметр в Complete/CompleteSimple |
| `internal/agent/plan.go` | Тип VerbosityLevel + String() + ParseVerbosity() |
| `internal/agent/coordinator.go` | Verbosity в конфиг + промпты; SectionReport; ExecutiveSummary; criticLoop с feedback |
| `internal/agent/analyst.go` | Verbosity + factTruncLen + analysisLengthGuide |
| `internal/agent/critic.go` | weak_sections в CritiqueResult; NewCriticAgent принимает verbosity |
| `internal/agent/search.go` | Verbosity + factCountGuide + maxSummaryLen + maxRawLen |
| `internal/memory/report.go` | ExecutiveSummary в Report; Complete принимает execSummary |
| `cmd/server/main.go` | VERBOSITY конфиг; проброс verbosity; WordCount; 404/error handlers; catch-all route |
| `cmd/server/web/templates/base.html` | — |
| `cmd/server/web/templates/index.html` | Event-log div |
| `cmd/server/web/templates/report_detail.html` | TOC, copy btn, word count; фикс бага с `$r.ReportHTML` |
| `cmd/server/web/templates/report_list.html` | — |
| `cmd/server/web/templates/not_found.html` | **Новый**: стилизованная 404 |
| `cmd/server/web/templates/error.html` | **Новый**: страница ошибки сервера |
| `cmd/server/web/static/style.css` | Полный редизайн: custom properties, тёмная тема, responsive, карточки, типографика, TOC, print |
| `cmd/server/web/static/main.js` | TOC, copy, nav-accent, event-log, SSE timeout, реструктуризация |
| `.env.example` | Добавлена VERBOSITY |
| `docs/Architech.md` | Описание VerbosityLevel, обновлены диаграммы, раздел 5.7 |
| `docs/Changelog.md` | **Новый**: журнал изменений |

#### Очистка зависших репортов и ручное завершение

- **Startup cleanup**: при запуске сервера все репорты со статусом `in_progress` автоматически помечаются `error` c сообщением "сервер был перезапущен"
- **POST /api/reports/{id}/fail**: ручное завершение зависшего репорта (ставит `error`, тело опционально: `{"error": "..."}`)
- **POST /api/reports/{id}/retry**: повторный запуск исследования для указанного репорта (создаётся новый репорт с тем же `Query`, возвращает `new_report_id`)

#### Ретраи embedding API

Переработана логика повторных попыток при ошибках embedding API (chromem):

- Добавлен вспомогательный метод `retryWithBackoff` с экспоненциальным backoff и джиттером (±50%)
- Обёрнуты `coll.AddDocument` (сохранение фактов) и `coll.Query` (поиск) — обе операции могут падать по 502
- Параметры:
  - `EMBEDDING_RETRY_ATTEMPTS=3` (наследуется от `LLM_RETRY_ATTEMPTS`)
  - `EMBEDDING_RETRY_BASE_DELAY=1s` (наследуется от `LLM_RETRY_BASE_DELAY`)

### Конфигурация

```bash
# Новая переменная
VERBOSITY=normal  # brief | normal | detailed
```

#### Логирование веб-панели

- Добавлены `log.Debug` во все page-хендлеры: главная, список отчётов, просмотр отчёта
- Каждое SSE-событие логируется: `SSE: событие <type>, report_id=<id>`
- `MALDER_LOG_LEVEL=debug` (или `export MALDER_LOG_LEVEL=debug`) для просмотра

#### Cache-Control для статики

- Обёртка `cacheControlMiddleware` над `http.FileServer` с заголовком `Cache-Control: no-cache, must-revalidate`
- Браузер перестаёт кешировать `style.css` и `main.js` — при следующем ребилде Docker-образа изменения подхватываются без Ctrl+F5

#### Ретраи LLM-клиента

Переработана логика повторных попыток при ошибках LLM API:

- **Таймаут фиксированный** (60s) на каждую попытку — больше не растёт экспоненциально
- **Backoff с джиттером** (crypto/rand, ±50%):
  - deadline/timeout → базовая задержка (1s по умолчанию)
  - 5xx → экспоненциальный backoff: 1s, 2s, 4s (с джиттером)
- **Параметры конфигурируемы**:
  - `LLM_RETRY_ATTEMPTS=3` — количество попыток
  - `LLM_RETRY_BASE_DELAY=1s` — базовая задержка между попытками
- **Максимальное время ожидания**: ~60+1+60+2+60+4 ≈ 187s (было ~60+60+120+240 = 480s для 3 попыток)
