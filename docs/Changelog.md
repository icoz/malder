# Changelog

## [Unreleased]

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

#### Веб-интерфейс

- На странице деталей отчёта отображается блок «Краткое резюме» (если есть)
- API `POST /api/research` возвращает `executive_summary` в JSON

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
| `cmd/server/main.go` | VERBOSITY конфиг; проброс verbosity в агенты |
| `cmd/server/web/templates/report_detail.html` | Отображение ExecutiveSummary |
| `.env.example` | Добавлена VERBOSITY |
| `docs/Architech.md` | Описание VerbosityLevel, обновлены диаграммы, раздел 5.7 |

### Конфигурация

```bash
# Новая переменная
VERBOSITY=normal  # brief | normal | detailed
```
