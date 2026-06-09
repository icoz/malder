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
 | `POST` | `/api/reports/{id}/fail` | Принудительно завершить ошибкой |
| `POST` | `/api/reports/{id}/retry` | Перезапустить исследование |
| `POST` | `/api/knowledge/upload` | Загрузка документа в базу знаний (multipart) |
| `GET` | `/api/knowledge/documents` | Список документов (JSON) |
| `GET` | `/api/knowledge/documents/{id}` | Метаданные документа (JSON) |
| `GET` | `/api/knowledge/documents/{id}/raw` | Markdown документа |
| `GET` | `/api/knowledge/export` | ZIP-архив всех документов |
| `DELETE` | `/api/knowledge/documents/{id}` | Удаление документа |
| `GET` | `/knowledge` | Страница базы знаний (HTML) |
| `GET` | `/api/health` | Healthcheck |

**Логгирование HTTP-запросов**: все хендлеры обёрнуты в `logPageHandler`, который логирует при выходе: `← WebUI.handlerName: GET /path → 200 (1.2s)`. Использует `logResponseWriter` для перехвата HTTP-статуса.

**Шаблоны**: каждый хендлер получает независимый экземпляр `*template.Template`, созданный через `base.Clone()` + парсинг собственного файла. Это предотвращает конфликт `{{define "content"}}` в общем namespace (исправление: `ParseFS(…, "web/templates/*.html")` приводил к тому, что алфавитно последний файл перезаписывал все остальные).

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
| `LLM_RETRY_ATTEMPTS` | `3` | Попыток LLM-вызова |
| `LLM_RETRY_BASE_DELAY` | `1s` | Базовая задержка между retry |
| `EMBEDDING_RETRY_ATTEMPTS` | `3` | Попыток эмбеддинг-вызова |
| `EMBEDDING_RETRY_BASE_DELAY` | `1s` | Базовая задержка между retry эмбеддинга |
| `VLM_ENDPOINT` | `LLM_ENDPOINT` | Endpoint VLM-модели (для описания изображений) |
| `VLM_API_KEY` | `LLM_API_KEY` | API-ключ VLM |
| `VLM_MODEL` | `ibm-granite-vision-7b` | Vision-language модель |
| `VLM_TIMEOUT` | `LLM_TIMEOUT` | Таймаут VLM-вызова |
| `KNOWLEDGE_PATH` | `./data/knowledge` | Путь к файлам базы знаний (markdown на диске) |

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
4. knowledgeSearchTool.Execute(ctx, {"query": userQuery})
   │ Поиск по базе знаний (chromem-коллекция `knowledge`)
   │ Результаты → memory.Save("knowledge: …") — AnalystAgent видит их
   │ наравне с веб-фактами через memory.Recall
   ▼
5. researchSubtopics(ctx, plan)
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

### 2.5 `internal/agent/document.go` — загрузка и обработка документов

**DocumentAgent** — конвертирует загруженные пользователем документы в markdown, извлекает и описывает изображения через VLM, чанкует текст и сохраняет в базу знаний.

**`Process(ctx, filePath, originalName, contentType) → (*DocumentMeta, error)`**:

```
1. detectContentType → docx/odt/pdf/epub/rtv/html/txt
2. pandoc filePath --to markdown --wrap=none --extract-media=TMPDIR
   │ Результат: output.md + директория TMPDIR/media/ с изображениями
   ▼
3. describeImages(markdown, TMPDIR)
   │ regexp `!\[.*?\]\((media/.*?)\)` → список изображений
   │ Для каждого:
   │   └─ shouldProcessImage → эвристика: min 80×80, ratio ≤ 20:1
   │       └─ VLM (CompleteVision, ibm-granite-vision-7b):
   │           "What is shown in the image? Describe in detail in Russian."
   │           → описание изображения
   │           → замена `![...](media/...)` на `[Описание изображения: ...]`
   ▼
4. chunkMarkdown(text, chunkSize=500, overlap=100)
   │ Разбивка по границам абзацев, целевой размер ~500 слов
   ▼
5. KnowledgeStore.Create(meta) + SaveChunkIDs
   └─ markdown на диск: {KnowledgePath}/docs/{docID}.md
   └─ Для каждого чанка: longTermMemory.SaveKnowledgeChunk()
       → chromem-коллекция `knowledge`
6. Очистка TMPDIR → return DocumentMeta
```

**VLM-клиент**: отдельный `llm.Client` с собственными endpoint/model/timeout (конфигурируется через `VLM_*`). По умолчанию `ibm-granite-vision-7b` — не зависит от основной LLM.

**Поддерживаемые форматы**: DOCX, ODT, PDF, EPUB, RTF, HTML, TXT (Pandoc). `.doc` (бинарный Word) — не поддерживается (требует LibreOffice).

---

### 2.6 `internal/agent/critic.go` — оценка качества

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

### 2.7 `internal/agent/plan.go` — типы плана и уровень детализации

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

### 2.8 `internal/memory/longterm.go` — векторная память (chromem)

**LongTermMemory** — обёртка над chromem-go для семантического поиска фактов.

**Хранение**: chromem persistent DB на диске (`MEMORY_PATH`). Коллекция `"facts"`. Каждый документ — факт с префиксом `Источник: {url}`.

**Эмбеддинги**: `chromem.NewEmbeddingFuncOpenAICompat` — совместимый с OpenAI API (modelgate.ru). Эндпоинт конфигурируется через `EMBEDDING_ENDPOINT`/`EMBEDDING_API_KEY`/`EMBEDDING_MODEL`.

**Ретраи эмбеддингов**: `Save()` и `RecallWithTopK()` обёрнуты в `retryWithBackoff()` (аналогично LLM-клиенту: jitter + exponential backoff). Конфигурируется через `EMBEDDING_RETRY_ATTEMPTS` / `EMBEDDING_RETRY_BASE_DELAY`.

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

### 2.9 `internal/memory/source.go` — provenance (bolt)

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

### 2.10 `internal/memory/report.go` — отчёты (bolt)

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

### 2.11 `internal/memory/knowledge.go` — база знаний (bbolt + диск)

**KnowledgeStore** — хранит метаданные загруженных документов и их markdown-содержимое.

| Хранилище | Назначение |
|-----------|-----------|
| bbolt (`knowledge_meta`) | `DocumentMeta` (ID, originalName, contentType, size, chunkCount, status, createdAt) |
| Диск (`{KnowledgePath}/docs/{docID}.md`) | Полный markdown-текст документа |

```go
type DocumentMeta struct {
    ID           string // UUID
    OriginalName string // имя загруженного файла
    ContentType  string // mime-тип
    Size         int64  // размер в байтах
    ChunkCount   int    // количество чанков в chromem
    Status       string // "ready" | "processing" | "error"
    Error        string // сообщение об ошибке
    CreatedAt    time.Time
}
```

**Методы**:

| Метод | Описание |
|-------|----------|
| `Create(meta, markdown) error` | Сохраняет метаданные в bbolt + markdown на диск |
| `Get(id) (*DocumentMeta, error)` | Метаданные из bbolt |
| `List() ([]DocumentMeta, error)` | Все документы (с сортировкой по дате) |
| `GetMarkdown(id) (string, error)` | Чтение markdown с диска |
| `Delete(id) error` | Удаление из bbolt + удаление файла с диска. **Не удаляет чанки из chromem** (chromem-go не имеет DeleteDocument) |
| `SaveChunkIDs(id, chunkIDs) error` | Сохраняет список chunkID в bbolt |
| `ExportArchive() ([]byte, error)` | ZIP-архив всех markdown-файлов |

---

### 2.12 `internal/llm/client.go` — LLM-клиент

**Config**:
```go
type Config struct {
    Endpoint string        // без /v1 — добавляется автоматом
    APIKey   string
    Timeout  time.Duration // per-attempt timeout
}
```

**Ретраи**: до 3 попыток (конфигурируется через `LLM_RETRY_ATTEMPTS`). Per-attempt таймаут — 60s (`LLM_TIMEOUT`). Между попытками jitter backoff (`crypto/rand`, ±50%): deadline exceeded → быстрый retry (1s), 5xx → exponential (1s, 2s, 4s). Базовая задержка настраивается через `LLM_RETRY_BASE_DELAY`.

**Логгирование**: каждый вызов `Complete()` получает `req_id` (12-символьный hex из `time.Now().UnixNano()`). Логи:
- Entry DEBUG с req_id, model, chars
- INFO на успешном ответе с таймингом
- WARN на каждой неудачной попытке с причиной
- Финальный WARN при исчерпании retry: model, chars, attempts, duration, last error, http_status, body_snippet

**Таймаут vs cancel**: при `context.DeadlineExceeded` возвращается `"timeout after %s"`, при `context.Canceled` — `"client disconnected"`. Позволяет отличить таймаут от ручной отмены (SSE-disconnect).

**`ChatMessage.Content`**: тип изменён с `string` на `any` — поддерживает как plain-text строки (обратно совместимо), так и content parts для vision-запросов.

**Методы**:
- `CompleteSimple(ctx, model, system, prompt, temperature, maxTokens) → (string, error)` — основной метод
- `Complete(ctx, model, messages, temperature, maxTokens) → (string, error)` — с произвольными сообщениями
- `CompleteVision(ctx, model, system, userText, base64Images, temperature, maxTokens) → (string, error)` — передаёт текст + base64-изображения как content parts (поддержка vision-моделей)

Параметр `maxTokens` управляет максимальной длиной ответа LLM (0 = не ограничено). Зависит от `VerbosityLevel`:
- brief: 512-2048
- normal: 2048-8192
- detailed: 4096-16384

**Endpoint**: к `Config.Endpoint` добавляется `/v1/chat/completions`. Embedding endpoint (для chromem) добавляет `/v1/embeddings`.

---

### 2.13 `internal/scheduler/adaptive.go` — адаптивный планировщик

**AdaptiveScheduler** — регулирует параллелизм поиска в зависимости от успешности запросов.

**Алгоритм**:
- Начальный параллелизм: `InitialMax`
- После каждого запроса: `Record(duration, err)`
- Если ошибка (особенно 429 Too Many Requests) — снижает параллелизм на 20%
- Если успешно — постепенно повышает до `MaxConcurrent`
- `WindowSize` — окно для расчёта средней latency
- `TargetLatency` — целевая задержка

---

### 2.14 `internal/tool/search.go` — OpenSERP

**SearchTool.Execute(ctx, args) → (string, error)**:
- Аргументы: `query`, `limit`
- URL: `{openserp_url}/{engine}/search?text={query}&limit={limit}`
- Формат ответа: v2 JSON → ручное форматирование в markdown-список (title/URL/domain/snippet)

**Поддерживаемые движки**: задаётся через `SEARCH_ENGINE` env var. По умолчанию `duck`. OpenSERP запускается в отдельном контейнере.

---

### 2.15 `internal/tool/fetch_page.go` — извлечение контента

**FetchPageTool.Execute(ctx, args) → (string, error)**:
- Аргументы: `url`
- Загрузка HTML через HTTP (User-Agent: `Mozilla/5.0...`)
- Извлечение читаемого контента через `go-readability` (article extraction)
- Конвертация HTML → markdown через `html-to-markdown`
- Возвращает очищенный текст статьи

---

### 2.16 `internal/tool/knowledge_search.go` — поиск по базе знаний

**KnowledgeSearchTool** — ищет релевантные чанки из загруженных документов в chromem-коллекции `knowledge`.

**`Execute(ctx, args) → (string, error)`**:
- Аргументы: `query` (строка поиска), `topK` (число результатов, default 5)
- Вызывает `longTermMemory.RecallKnowledge(ctx, query, topK)` — векторный поиск по коллекции `knowledge`
- Возвращает markdown-список чанков с префиксом `[Источник: {document_name}]`

**Интеграция**: координатор вызывает `knowledgeSearchTool.Execute({"query": userQuery})` после веб-поиска и перед анализом подтем. Результаты сохраняются через `memory.Save("knowledge: …")` — AnalystAgent подхватывает их через обычный `memory.Recall`.

---

### 2.17 `internal/log/logger.go` — логирование

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
  ├─ POST /api/knowledge/upload → загрузка документа
  │   └─ DocumentAgent.Process → Pandoc → VLM → чанкинг
  │       ├─ KnowledgeStore.Create → bbolt + markdown на диск
  │       └─ longTermMemory.SaveKnowledgeChunk → chromem `knowledge`
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
              │    ├─ memory.Save(raw) → chromem `facts` + sourceStore
              │    └─ memory.Save(summary) → chromem `facts` + sourceStore
              │  knowledgeSearchTool.Execute → chromem `knowledge`
              │    └─ memory.Save("knowledge: …") → chromem `facts`
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
- **Две коллекции**:
  - `"facts"` — факты из веб-поиска (SearchAgent, AnalystAgent, Coordinator)
  - `"knowledge"` — чанки из загруженных документов (DocumentAgent → KnowledgeSearchTool)
- Эмбеддинги: OpenAI-compatible API
- Поиск: cosine similarity, top-K
- Используется для: семантического поиска фактов при анализе, проверке кеша и поиске по базе знаний

### bolt (структурированная)
- Файловая: `SOURCE_STORE_PATH` — один bolt файл на три store
- **Три bucket**: `"provenance"`, `"reports"`, `"knowledge_meta"`
- `SourceStore` — provenance chains (page → subreport → section → final)
- `ReportStore` — completed/in_progress/error reports
- `KnowledgeStore.knowledge_meta` — DocumentMeta (метаданные загруженных документов в базу знаний)

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

### 5.8 KB как дополнительный источник (не замена вебу)

База знаний не заменяет веб-поиск, а дополняет его: факты из документов подмешиваются через `memory.Save("knowledge: …")` после поиска в интернете. AnalystAgent видит их наравне с веб-фактами — для аналитика нет разницы между источником. KB-факты попадают в ту же chromem-коллекцию `facts`, но с префиксом `"knowledge: "` для отслеживания.

### 5.9 VLM — отдельный клиент

VLM-клиент (`ibm-granite-vision-7b`) запускается в отдельном `llm.Client` с собственными endpoint/model/timeout. Это позволяет:
- тюнить таймаут независимо (изображения обрабатываются дольше)
- не расходовать токены основной LLM на описание картинок
- не смешивать system prompt координатора/аналитика с vision-запросами

### 5.10 Markdown на диске, метаданные в bbolt

Markdown-содержимое документов хранится на диске (`{KnowledgePath}/docs/{id}.md`), а не в bbolt. Мотивация:
- PDF на 200+ страниц не нагружает bbolt-транзакции
- Экспорт в ZIP — просто чтение файлов с диска, без десериализации
- Можно просматривать/редактировать вручную

### 5.11 Pandoc как внешний конвертер

Pandoc вызывается через `os/exec`, а не через Go-библиотеку. Покрывает все поддерживаемые форматы (DOCX, ODT, PDF, EPUB, RTF, HTML, TXT) одной командой. `--extract-media=TMPDIR` извлекает изображения в поддиректорию `media/`.

### 5.12 Отсутствие DeleteDocument в chromem

chromem-go v0.5.0 не поддерживает удаление отдельных документов. При удалении документа из базы знаний:
- метаданные удаляются из bbolt (`knowledge_meta`)
- markdown удаляется с диска
- **чанки остаются в chromem-коллекции `knowledge`** (безвредно, но занимают место)

Возможные решения: удалять всю коллекцию `knowledge` при редких операциях очистки, или обновить chromem при появлении `DeleteDocument`.

### 5.13 Отчёты с full lifecycle
Report отслеживает полный lifecycle: `in_progress` → `completed`/`error`. На странице деталей — in-place polling (fetch каждые 3с, обновление progress-секции через DOM, reload только при финальном статусе). При старте сервера все зависшие `in_progress` отчёты автоматически переводятся в `error` (`FailInProgressReports`).

---

## 6. Веб-интерфейс

- Шаблоны: `html/template` (Go 1.23), встроены через `//go:embed`
- Каждый хендлер получает свой экземпляр `*template.Template` через `base.Clone() + page file` (предотвращает namespace conflict `{{define "content"}}`)
- Статика: CSS + JS (SSE-клиент), тоже embedded
- Markdown → HTML: goldmark на сервере (никаких JS-библиотек)
- Формат дат: русскоязычный через template function `russianDate`
- SSE-клиент на странице исследования: прогресс-бар, авто-редирект на страницу отчёта
- Страница деталей отчёта: in-place polling `/api/reports/{id}` раз в 3с при `status=in_progress`. Если статус изменился — полный reload страницы. Иначе обновление progress-секции через DOM (название фазы, план, счётчики completed/total, critic score/iteration).
- `Cache-Control: no-cache, must-revalidate` на `/static/`
- Logging: все хендлеры обёрнуты в `logPageHandler` (пишет `← WebUI.handlerName: GET /path → 200 (1.2s)` при выходе)
