# Архитектура Malder

Malder — multi-agent система для автоматизированного глубокого исследования (deep research) произвольных тем через интернет.

---

## 1. Общий обзор

Пользователь вводит тему → система составляет структурированный план исследования → выполняет поиск в интернете → анализирует каждую подтему → синтезирует разделы → формирует итоговый отчёт → проверяет через критика (с возможными итерациями доработки). Весь процесс отслеживается: отчёты сохраняются в bolt, каждый факт привязан к источнику через цепочку provenance.

```
  POST /api/research  GET /api/research/stream?q=...
          │                    │
          ▼                    ▼
  ┌────────────────────────────────────────────────────┐
  │                   Coordinator                      │
  │                                                    │
  │  1. createPlan(query) → ResearchPlan (LLM, JSON)   │
  │  2. searchAgent.Run(allQueries)                    │
  │  3. researchSubtopics(plan) — параллельно          │
  │     AnalystAgent.GenerateSubReport() per subtopic  │
  │  4. synthesizeSections — параллельно               │
  │  5. criticLoop (с итеративным углублением):        │
  │     ├─ synthesizeFinal → черновик отчёта           │
  │     ├─ criticAgent.Evaluate → score + weak_sections│
  │     ├─ score<7 → доп. поиск + feedback → повтор    │
  │     └─ feedback передаётся в следующий синтез      │
  │  6. [опционально] generateExecutiveSummary         │
  │  7. return ResearchResult{Report, SourceURLs}      │
  └────────────────────────────────────────────────────┘
            │                           ▲
            ▼                           │
  ┌────────────────────┐       ┌──────────────────┐
  │   SearchAgent      │       │   AnalystAgent   │
  │  ┌───────────────┐ │       │  ┌──────────────┐│
  │  │ cache check   │─┼──────►│ LLM: generate   ││
  │  │ ↓ OpenSERP    │ │       │  │ subreport    ││
  │  │ ↓ fetch+summ  │ │       │  └──────────────┘│
  │  │ ↓ save to     │ │       │ + SaveFactTool   │
  │  │   chromem+bolt│ │       │ + SourceStore    │
  │  └───────────────┘ │       └──────────────────┘
  └────────────────────┘
            │
            ▼
  ┌──────────────────────────────────────────────┐
  │               CriticAgent                    │
  │  LLM: оценивает отчёт по 10-балльной шкале,  │
  │  даёт развёрнутый фидбек                     │
  └──────────────────────────────────────────────┘
```

---

## 2. Компоненты

### 2.1 `cmd/server/main.go` — точка входа и HTTP-сервер

**Назначение**: загрузка конфигурации из env, инициализация всех компонентов, настройка роутинга.

**Роутинг** (Go 1.23 ServeMux):

| Метод | Путь | Назначение |
|-------|------|-----------|
| `GET` | `/` | Форма исследования (HTML) |
| `GET` | `/reports` | Список отчётов (HTML) |
| `GET` | `/reports/{id}` | Детали отчёта, markdown→HTML |
| `GET` | `/api/reports` | Список отчётов (JSON) |
| `GET` | `/api/reports/{id}` | JSON одного отчёта (для polling) |
| `GET` | `/api/reports/{id}/raw` | Сырой markdown |
| `POST` | `/api/research` | Синхронное исследование |
| `GET` | `/api/research/stream?q=` | SSE-исследование с прогрессом |
| `GET` | `/api/health` | Healthcheck |

**SSE-рукопожатие** (`apiSSEResearchHandler`):
1. Создаётся report в `ReportStore` (status=in_progress)
2. Отправляется `event: started` с `report_id`
3. В горутине создаётся `tempCoord` (копия конфига, свежий инстанс координатора)
4. Через `ProgressReporter` шлются события: `planning`, `search_start`, `subtopic_analysis_start`, `section_synthesis_start`, `critic_loop_start`, `synthesis_start`, `critic_start`, `additional_search_start`, `finish`
5. При завершении: `reportStore.Complete(...)` и `event: result`
6. При ошибке/отмене: `reportStore.Fail(...)` и `event: error`/`event: cancelled`

**Веб-интерфейс**: шаблоны Go `html/template` в `web/templates/`, статика в `web/static/`, встроены через `//go:embed`. Markdown → HTML через goldmark. Формат даты — русскоязычный ("8 июня 2026, 15:04").

**Конфигурация**: все параметры из `os.Getenv`:

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `LLM_ENDPOINT` | `https://api.modelgate.ru` | Базовый LLM endpoint |
| `LLM_API_KEY` | — | API-ключ |
| `LLM_MODEL` | `deepseek-v4-flash` | Модель по умолчанию |
| `LLM_TEMPERATURE` | `0.7` | Температура |
| `LLM_TIMEOUT` | `60s` | Таймаут попытки |
| `LLM_ENDPOINT_COORDINATOR` | `LLM_ENDPOINT` | Per-агент endpoint (аналогично для ANALYST, CRITIC) |
| `LLM_MODEL_COORDINATOR` | `LLM_MODEL` | Per-агент модель (аналогично для ANALYST, CRITIC) |
| `OPENSERP_URL` | `http://localhost:8080` | URL OpenSERP |
| `SEARCH_ENGINE` | `duck` | Поисковый движок |
| `MEMORY_PATH` | `./data/malder_memory` | Путь к chromem |
| `SOURCE_STORE_PATH` | `{MEMORY_PATH}_sources.db` | Путь к bolt |
| `SERVER_PORT` | `8080` | Порт |
| `MAX_CONCURRENT_SEARCH` | `3` | Начальный параллелизм поиска |
| `MAX_PAGES_PER_QUERY` | `3` | Страниц на запрос |
| `MIN_RELEVANT_FACTS` | `10` | Минимум фактов для пропуска поиска |
| `RECALL_TOP_K` | `15` | Сколько фактов возвращает векторный поиск |
| `RECALL_DISTANCE_THRESHOLD` | `0.5` | Порог средней косинусной дистанции |
| `RECALL_LLM_CHECK` | `true` | LLM-проверка кеша перед пропуском |
| `VERBOSITY` | `normal` | Уровень детализации: `brief`, `normal`, `detailed` |
| `MAX_ITERATIONS` | `3` | Максимум итераций критика |
| `MAX_CONCURRENT_SUBTOPICS` | `3` | Параллелизм анализа подтем |
| `MAX_SUBTOPIC_RETRIES` | `2` | Попыток дозапроса по gap-запросам |
| `MALDER_LOG_LEVEL` | `info` | Уровень логирования (debug/info/warn/error) |

---

### 2.2 `internal/agent/coordinator.go` — оркестратор

**CoordinatorAgent** — главный управляющий. Получает запрос пользователя и проводит через полный пайплайн.

**Метод `Run(ctx, userQuery) → *ResearchResult`**:

```
1. createPlan(userQuery)
   │ LLM → ResearchPlan (title + sections[].subtopics[].queries[])
   ▼
2. flattenQueries → allQueries[]
   ▼
3. searchAgent.Run(ctx, allQueries)
   │ Параллельный поиск + суммаризация страниц
   ▼
4. researchSubtopics(ctx, plan)
   │ Для каждой подтемы (параллельно, семофор maxConcurrentSubtopics):
   │   AnalystAgent.GenerateSubReport()
   │   если !complete → gap-запросы → searchAgent.Run → повтор (до maxSubtopicRetries)
   ▼
5. synthesizeSections(ctx, plan, subReports)
   │ Для каждой секции (параллельно):
   │   LLM: merge анализов подтем в связный раздел
   │   saveToMemory + sourceStore.Put
   ▼
6. criticLoop(ctx, title, sectionReports)
   │ Итерации (до maxIterations):
   │   a. synthesizeFinal → LLM: intro + разделы + transitions + conclusion
   │      (фидбек с предыдущей итерации передаётся в промпт)
   │   b. criticAgent.Evaluate → score + weak_sections
   │   c. если score >= 7 → break
   │   d. extractQueriesFromFeedback → доп. поиск
   │   e. (возврат к a)
   ▼
7. [detailed] generateExecutiveSummary(ctx, title, fullReport)
   ▼
8. return ResearchResult{Report, ExecutiveSummary, SourceURLs}
```

**ProgressReporter**: функция `(event string, data map[string]any)`, вызывается на каждом этапе для SSE-трансляции клиенту.

**ProgressSaver**: аналогичный callback, сохраняет те же данные в `ReportStore.SaveProgress(id, event, data)` (JSON-слияние в поле `RawProgress`). Используется для восстановления состояния на странице деталей отчёта при перезагрузке (in-place polling без full page reload).

**Ключевое решение**: координатор не использует `context.WithTimeout` — таймауты управляются исключительно LLM-клиентом (per-attempt + retry backoff). Agent-level timeout конфликтовал с ретраями.

---

### 2.3 `internal/agent/search.go` — поиск и кеширование

**SearchAgent** — выполняет поиск в интернете, загружает страницы, суммаризирует их, сохраняет в память.

#### Логика пропуска поиска (кеш)

```
processQueryInternal(query):
  1. facts, avgDist = memory.RecallWithTopK(query, topK)
     │ chromem: vector similarity search, topK=15 (RECALL_TOP_K)
     │ avgDist = 1 - avg(cosine_similarity)
     ▼
  2. len(facts) >= minRelevantFacts (10)?
     ├─ НЕТ → интернет-поиск
     └─ ДА  → 3. avgDist <= distanceThreshold (0.5)?
              ├─ НЕТ → поиск (факты есть, но слабо релевантны)
              └─ ДА  → 4. useLLMCheck?
                       ├─ false → пропустить поиск
                       └─ true  → 5. llmCheck(query, facts):
                                  ├─ LLM: "Достаточно ли этих фактов?" YES/NO
                                  ├─ YES → пропустить поиск
                                  └─ NO  → интернет-поиск
```

`llmCheck` передаёт до 5 фактов в summarizer LLM. Если LLM недоступен → `true` (кеш считается достаточным, пропускаем поиск).

#### Пайплайн загрузки страницы

```
processPage(url, query):
   1. fetchTool.Execute(url) → readability extraction → markdown
   2. Save RAW: memory.Save(rawKey, "Источник: {url}\n...")
      sourceStore.Put(Kind:"page", IsRaw:true)
      (длина raw зависит от VERBOSITY: 5K / 10K / 20K)
   3. summarizeContent(content) → LLM extracts факты
      (количество фактов зависит от VERBOSITY: 3-5 / 3-7 / 7-15)
   4. Save SUMMARY: memory.Save(summaryKey, "Источник: {url}\nСуть:\n...")
      sourceStore.Put(Kind:"page", IsRaw:false, Parents:[rawKey])
      (длина summary зависит от VERBOSITY: 1.5K / 3K / 6K)
```

**Параллелизм**: поиск по запросам через семофор (`scheduler.GetMaxConcurrent()`), загрузка страниц — не более 2 одновременно (хардкод).

---

### 2.4 `internal/agent/analyst.go` — анализ подтем

**AnalystAgent** — для каждой подтемы получает факты из памяти и просит LLM составить аналитическую заметку.

**`GenerateSubReport(ctx, sectionName, subtopicName, topic) → *SubReport`**:

```
1. memory.Recall(ctx, subtopicName) — все факты по подтеме
2. LLM prompt: topic + subtopic + facts → JSON
   {
     "analysis": "...",       // текст заметки
     "complete": true/false,  // достаточно ли фактов
     "gap_queries": [...],    // если не хватает — запросы для дозапроса
     "gap_reason": "..."      // объяснение
   }
3. Save to memory + sourceStore (Kind:"subreport")
4. Return SubReport
```

Если `complete=false`, координатор выполняет gap-запросы и повторяет вызов (до `maxSubtopicRetries`).

---

### 2.5 `internal/agent/critic.go` — оценка качества

**CriticAgent** — оценивает итоговый отчёт по 10-балльной шкале.

**`Evaluate(ctx, report) → (score, feedback, weakSections, error)`**:

```
LLM prompt: оцени отчёт по критериям:
- полнота + глубина (40%)
- точность и факты (30%)
- структура и читаемость (20%)
- использование источников (10%)
→ JSON {
    "score": 7,
    "feedback": "...",
    "weak_sections": ["Раздел 1", "Раздел 3"]
  }
```

Если score >= 7, цикл критика прерывается досрочно. Иначе координатор:
1. Извлекает gap-запросы из фидбека → дополнительный поиск
2. Передаёт фидбек в `synthesizeFinal` для учёта замечаний
3. Синтезирует новую версию с учётом фидбека и новых фактов

---

### 2.6 `internal/agent/plan.go` — типы плана и уровень детализации

```go
type VerbosityLevel int

const (
    VerbosityBrief VerbosityLevel = iota
    VerbosityNormal
    VerbosityDetailed
)
```

`VerbosityLevel` управляет масштабом исследования и глубиной каждого этапа:

| Аспект | brief | normal | detailed |
|--------|-------|--------|----------|
| Секций в плане | 1-2 | 2-4 | 4-6 |
| Подтем на секцию | 1-2 | 2-3 | 3-5 |
| Запросов на подтему | 1-2 | 2-3 | 3-4 |
| Фактов на страницу | 3-5 | 3-7 | 7-15 |
| Длина raw страницы | до 5K символов | до 10K | до 20K |
| Длина суммаризации | до 1.5K символов | до 3K | до 6K |
| Раздел отчёта | 150-300 слов | содержательный | 800-1500 слов |
| Финальный отчёт | до 2K символов | до 8K | до 16K |
| Executive summary | нет | нет | да |

```go
type ResearchPlan struct {
    Title    string    `json:"title"`
    Sections []Section `json:"sections"`
}
type Section struct {
    Name      string     `json:"name"`
    Subtopics []Subtopic `json:"subtopics"`
}
type Subtopic struct {
    Name    string   `json:"name"`
    Queries []string `json:"queries"`
}
```

---

### 2.7 `internal/memory/longterm.go` — векторная память (chromem)

**LongTermMemory** — обёртка над chromem-go для семантического поиска фактов.

**Хранение**: chromem persistent DB на диске (`MEMORY_PATH`). Коллекция `"facts"`. Каждый документ — факт с префиксом `Источник: {url}`.

**Эмбеддинги**: `chromem.NewEmbeddingFuncOpenAICompat` — совместимый с OpenAI API (modelgate.ru). Эндпоинт конфигурируется через `EMBEDDING_ENDPOINT`/`EMBEDDING_API_KEY`/`EMBEDDING_MODEL`.

**Ключевые методы**:

| Метод | Сигнатура | Описание |
|-------|-----------|----------|
| `Save` | `(ctx, key, value) error` | Сохраняет факт в карту `kv` + chromem |
| `Load` | `(key) (string, bool)` | Читает из карты `kv` |
| `Recall` | `(ctx, query) ([]string, error)` | Векторный поиск топ-K фактов, возвращает содержимое |
| `RecallWithTopK` | `(ctx, query, topK) ([]string, float64, error)` | То же, но с явным K + возвращает среднюю дистанцию |
| `TopK` | `() int` | Возвращает настроенное K |

**Дистанция**: chromem возвращает `Similarity` (cosine similarity, -1..1). `RecallWithTopK` конвертирует в расстояние: `avgDistance = 1 - avg(similarity)`. Дистанция 0 = идентично, 1 = ортогонально, 2 = противоположно.

---

### 2.8 `internal/memory/source.go` — provenance (bolt)

**SourceStore** — bolt-база для отслеживания происхождения каждого факта.

**Bucket**: `"provenance"`

**Тип `Provenance`**:
```go
type Provenance struct {
    Key       string   // уникальный ключ
    Kind      string   // "page" | "section" | "subreport" | "final"
    SourceURL string   // URL источника (только для page)
    Parents   []string // цепочка родителей (для summary → raw)
    Preview   string   // первые 200 символов
    IsRaw     bool     // оригинал или LLM-обработка
    Timestamp int64    // время создания
}
```

**Методы**: `Put`, `Get`, `GetChain` (рекурсивный обход родителей), `GetSourceURLs` (сбор всех SourceURL из цепочки), `ListByKind`.

**Использование**: факты от search-агента (raw + summary), от аналитика (subreport), от координатора (section, final). Позволяет для любого факта восстановить полную цепочку до оригинальных URL.

---

### 2.9 `internal/memory/report.go` — отчёты (bolt)

**ReportStore** — bolt-база для сохранения завершённых исследований.

**Bucket**: `"reports"`

**Тип `Report`**:
```go
type Report struct {
    ID               string       // UUID
    Query            string       // запрос пользователя
    Status           ReportStatus // in_progress | completed | error
    ReportText       string       // текст итогового отчёта
    ExecutiveSummary string       // краткое резюме (только для detailed)
    Error            string       // сообщение об ошибке
    SourceCount      int          // количество источников
    SourceURLs       []string     // список URL источников
    CreatedAt        int64        // время создания
    CompletedAt      *int64       // время завершения
    DurationMs       int64        // длительность в миллисекундах
}
```

**Методы**: `Create`, `Complete`, `Fail`, `Get`, `List`, `SaveProgress`, `FailInProgressReports`. `List()` возвращает метаданные без `ReportText`.

---

### 2.10 `internal/llm/client.go` — LLM-клиент

**Config**:
```go
type Config struct {
    Endpoint string        // без /v1 — добавляется автоматом
    APIKey   string
    Timeout  time.Duration // per-attempt timeout
}
```

**Ретраи**: до 3 попыток с экспоненциальным таймаутом: `timeout`, `timeout×2`, `timeout×4`. Между попытками такой же exponential backoff.

**Методы**:
- `CompleteSimple(ctx, model, system, prompt, temperature, maxTokens) → (string, error)` — основной метод
- `Complete(ctx, model, messages, temperature, maxTokens) → (string, error)` — с произвольными сообщениями

Параметр `maxTokens` управляет максимальной длиной ответа LLM (0 = не ограничено). Зависит от `VerbosityLevel`:
- brief: 512-2048
- normal: 2048-8192
- detailed: 4096-16384

**Endpoint**: к `Config.Endpoint` добавляется `/v1/chat/completions`. Embedding endpoint (для chromem) добавляет `/v1/embeddings`.

---

### 2.11 `internal/scheduler/adaptive.go` — адаптивный планировщик

**AdaptiveScheduler** — регулирует параллелизм поиска в зависимости от успешности запросов.

**Алгоритм**:
- Начальный параллелизм: `InitialMax`
- После каждого запроса: `Record(duration, err)`
- Если ошибка (особенно 429 Too Many Requests) — снижает параллелизм на 20%
- Если успешно — постепенно повышает до `MaxConcurrent`
- `WindowSize` — окно для расчёта средней latency
- `TargetLatency` — целевая задержка

---

### 2.12 `internal/tool/search.go` — OpenSERP

**SearchTool.Execute(ctx, args) → (string, error)**:
- Аргументы: `query`, `limit`
- URL: `{openserp_url}/{engine}/search?text={query}&limit={limit}`
- Формат ответа: v2 JSON → ручное форматирование в markdown-список (title/URL/domain/snippet)

**Поддерживаемые движки**: задаётся через `SEARCH_ENGINE` env var. По умолчанию `duck`. OpenSERP запускается в отдельном контейнере.

---

### 2.13 `internal/tool/fetch_page.go` — извлечение контента

**FetchPageTool.Execute(ctx, args) → (string, error)**:
- Аргументы: `url`
- Загрузка HTML через HTTP (User-Agent: `Mozilla/5.0...`)
- Извлечение читаемого контента через `go-readability` (article extraction)
- Конвертация HTML → markdown через `html-to-markdown`
- Возвращает очищенный текст статьи

---

### 2.14 `internal/log/logger.go` — логирование

**Уровни**: `DEBUG` → `INFO` → `WARN` → `ERROR`.

- `MALDER_LOG_LEVEL` — порог отображения (default `info`)
- `MALDER_DEBUG_FILE` — файл для debug-логов (отдельно от stdout)
- `Recover` — panic recovery с логгированием
- Все функции с enter/exit tracing на уровне DEBUG

---

## 3. Поток данных

```
HTTP-запрос
  │
  ├─ POST /api/research → синхронно
  │   └─ ReportStore.Create → Coordinator.Run → ReportStore.Complete
  └─ GET /api/research/stream → SSE
      └─ ReportStore.Create → (горутина) Coordinator.Run
                              → ReportStore.Complete/Fail
                              → event: result / event: error
                      │
                      ▼
              Coordinator.Run
              │  createPlan → LLM → ResearchPlan
              │  searchAgent.Run → cache check → OpenSERP → processes pages
              │    ├─ memory.Save(raw) → chromem + sourceStore
              │    └─ memory.Save(summary) → chromem + sourceStore
              │  researchSubtopics → AnalystAgent (LLM) → SubReport
              │  synthesizeSections → LLM → section report → memory + sourceStore
              │  criticLoop → LLM → final report → memory + sourceStore
              │    ├─ feedback → next synthesis iteration
              │    └─ score≥7 → break
              ├─ [detailed] generateExecutiveSummary → LLM → exec summary
              ▼
        ResearchResult{Report, ExecutiveSummary, SourceURLs}
              │
              ▼
        ReportStore.Complete(id, text, execSummary, urls, duration)
```

---

## 4. База данных

### chromem (векторная)
- Файловая: `MEMORY_PATH` — persistent chromem DB
- Коллекция: `"facts"`
- Эмбеддинги: OpenAI-compatible API
- Поиск: cosine similarity, top-K
- Используется для: семантического поиска фактов при анализе и проверке кеша

### bolt (структурированная)
- Файловая: `SOURCE_STORE_PATH` — один bolt файл на оба store
- **Два bucket**: `"provenance"` + `"reports"`
- `SourceStore` — provenance chains (page → subreport → section → final)
- `ReportStore` — completed/in_progress/error reports

### Долговременное хранение (in-memory KV)
- `LongTermMemory.kv` — `map[string]string`, дублирует chromem для быстрого доступа `Load()`
- Chromem отвечает за векторный поиск, KV — за прямой доступ по ключу

---

## 5. Ключевые архитектурные решения

### 5.1 Per-агент LLM конфигурация
Каждый агент (координатор, аналитик, критик) имеет собственный `llm.Client` с независимыми endpoint, API key, model, timeout. Позволяет направлять разные этапы на разные модели (дешёвая для планирования, дорогая для анализа, быстрая для критика).

### 5.2 Нет agent-level timeout
Убран `context.WithTimeout` на уровне агентов — он обрезал ретраи LLM-клиента. Единственный таймаут — per-attempt timeout в `llm.Config`, ретраи с экспоненциальным увеличением.

### 5.3 Трёхуровневая проверка кеша
Вместо простого счёта фактов: количественный порог → средняя семантическая дистанция → LLM-верификация. Предотвращает ложное срабатывание кеша на нерелевантных данных.

### 5.4 Provenance chains
Каждый факт сохраняет ссылки на родителей (raw → summary → subreport → section → final). Для любой секции можно восстановить исходные URL через `GetSourceURLs()`.

### 5.5 SSE с реальным SourceStore
В SSE-хендлере tempCoord получает настоящий `SourceStore` (не nil), что позволяет отслеживать provenance даже в streaming-режиме.

### 5.6 Адаптивный параллелизм
Scheduler динамически регулирует количество одновременных поисковых запросов на основе успешности и latency (особенно для 429-ответов).

### 5.7 Управление детализацией (VerbosityLevel)
Система поддерживает три уровня детализации (`brief`/`normal`/`detailed`), которые влияют на:
- **Масштаб плана**: количество секций, подтем и поисковых запросов
- **Длину промптов**: указание целевого объёма (в словах/абзацах)
- **MaxTokens**: ограничение максимального размера ответа LLM
- **Лимиты на сырые данные**: размер сохраняемого контента и суммаризации
- **Critic loop**: порог оценки и глубина доработки
- **Executive summary**: генерируется только для detailed-режима

### 5.8 Отчёты с full lifecycle
Report отслеживает полный lifecycle: `in_progress` → `completed`/`error`. На странице деталей — in-place polling (fetch каждые 3с, обновление progress-секции через DOM, reload только при финальном статусе). При старте сервера все зависшие `in_progress` отчёты автоматически переводятся в `error` (`FailInProgressReports`).

---

## 6. Веб-интерфейс

- Шаблоны: `html/template` (Go 1.23), встроены через `//go:embed`
- Статика: CSS + JS (SSE-клиент), тоже embedded
- Markdown → HTML: goldmark на сервере (никаких JS-библиотек)
- Формат дат: русскоязычный через template function `russianDate`
- SSE-клиент на странице исследования: прогресс-бар, авто-редирект на страницу отчёта
- Автообновление на странице деталей: polling `/api/reports/{id}` раз в 3 секунды при `status=in_progress`
