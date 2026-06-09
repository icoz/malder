# Сбор информации из локальных документов

## Реализовано

### KnowledgeStore — `internal/memory/knowledge.go`

- bbolt-таблица `knowledge_meta` для метаданных документов (`DocumentMeta`)
- Markdown-файлы хранятся на диске в `{KNOWLEDGE_PATH}/docs/{doc_id}.md`
- Методы: Create, Get, List, GetMarkdown, Delete, SaveChunkIDs, ExportArchive (ZIP)
- Чанки сохраняются в chromem-коллекцию `knowledge` через `LongTermMemory.SaveKnowledgeChunk`

### DocumentAgent — `internal/agent/document.go`

1. Pandoc: документ → markdown (`--extract-media` для извлечения изображений)
2. Regexp `!\[.*?\]\((media/.*?)\)` — сбор всех ссылок на изображения
3. VLM (CompleteVision) для каждого изображения:
   - Используется `ibm-granite-vision-7b` (modelgate)
   - Эвристика: min 80×80, не экстремальный ratio
   - Замена `![image](...)` на текстовое описание
4. Чанкинг: по ~500 слов с перекрытием ~100 слов
5. Сохранение: метаданные + markdown → KnowledgeStore, чанки → chromem

### KnowledgeSearchTool — `internal/tool/knowledge_search.go`

- tool `knowledge_search` для вызова из LLM-агентов
- Выполняет `memory.RecallKnowledge(query, topK)` по коллекции `knowledge`
- Используется в CoordinatorAgent после веб-поиска как дополнительный источник

### Интеграция в CoordinatorAgent

- Поле `knowledgeSearchTool` в `CoordinatorConfig`
- В `Run()` после `searchAgent.Run()` и перед `subtopic_analysis_start`:
  `kbSearchTool.Execute({"query": userQuery})` → `memory.Save("knowledge: ...")`
- KB-факты видны AnalystAgent через `memory.Recall` наравне с веб-фактами

### API endpoints

| Метод | Путь | Описание |
|-------|------|----------|
| `POST` | `/api/knowledge/upload` | multipart, загрузка документа |
| `GET` | `/api/knowledge/documents` | JSON-список документов |
| `GET` | `/api/knowledge/documents/{id}` | JSON метаданных |
| `GET` | `/api/knowledge/documents/{id}/raw` | Markdown документа |
| `GET` | `/api/knowledge/export` | ZIP-архив всех md |
| `DELETE` | `/api/knowledge/documents/{id}` | Удаление документа |
| `GET` | `/knowledge` | WebUI страница |

### LLM client — поддержка vision

- `ChatMessage.Content` изменён с `string` на `any` для content parts
- Добавлен `CompleteVision(ctx, model, systemPrompt, userText, base64Images, temperature, maxTokens)`

### Dockerfile

```dockerfile
RUN apk --no-cache add ca-certificates pandoc poppler-utils
```

### Config (env vars)

| Env | Default | Описание |
|-----|---------|----------|
| `VLM_ENDPOINT` | наследует `LLM_ENDPOINT` | Vision API |
| `VLM_API_KEY` | наследует `LLM_API_KEY` | |
| `VLM_MODEL` | `ibm-granite-vision-7b` | |
| `VLM_TIMEOUT` | `30s` | |
| `KNOWLEDGE_PATH` | `./data/malder_knowledge` | Путь к KB |

## Оставшиеся возможности (на будущее)

- **Удаление чанков из chromem** — API chromem-go v0.5.0 не имеет DeleteDocument, только DeleteCollection
- **LibreOffice для .doc** — если потребуется поддержка старого формата
- **PDF-изображения через poppler-utils** — Pandoc конвертирует PDF в md, но изображения из PDF извлекаются только через `pdfimages`
- **WebUI-страница документа** — просмотр markdown и метаданных в браузере

