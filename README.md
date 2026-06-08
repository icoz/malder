# malder – My Local Deep Research (MLDR)

Мультиагентная система для глубокого исследования тем с использованием LLM и веб-поиска (OpenSERP).

**Процесс**: пользователь вводит тему → LLM составляет план исследования → поиск в интернете → анализ каждой подтемы → синтез разделов → итеративная проверка критиком → готовый отчёт с источниками.

## Запуск

### Требования
- Docker и Docker Compose
- Доступ к OpenAI-совместимому LLM API (по умолчанию https://api.modelgate.ru)
- OpenSERP (запускается автоматически в docker-compose)

### Быстрый старт

```bash
git clone <repo>
cd malder
echo "LLM_API_KEY=your-key-here" > .env
docker-compose up --build
```

Откройте http://localhost:8080 в браузере.

### Конфигурация

Создайте `.env` файл (см. `docker-compose.yml` для полного списка):

```
LLM_API_KEY=sk-...
LLM_ENDPOINT=https://api.modelgate.ru
LLM_MODEL=deepseek-v4-flash
SEARCH_ENGINE=duck
```

### API endpoints

**Пользовательские (HTML):**

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/` | Веб-интерфейс (форма запроса) |
| `GET` | `/reports` | Список всех отчётов |
| `GET` | `/reports/{id}` | Детальный отчёт (markdown → HTML) |

**API (JSON / data):**

| Метод | Путь | Описание |
|-------|------|----------|
| `POST` | `/api/research` | Синхронное исследование |
| `GET` | `/api/research/stream?q=` | Исследование с SSE-прогрессом |
| `GET` | `/api/reports` | Список отчётов (JSON) |
| `GET` | `/api/reports/{id}` | Детали отчёта (JSON) |
| `GET` | `/api/reports/{id}/raw` | Сырой markdown отчёта |
| `GET` | `/api/health` | Проверка статуса |

Пример запроса:

```bash
curl -X POST http://localhost:8080/api/research \
  -H "Content-Type: application/json" \
  -d '{"query":"что такое горутины"}'
```

SSE-поток:

```bash
curl -N http://localhost:8080/api/research/stream?q=горутины
```

### Переменные окружения

**LLM:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `LLM_ENDPOINT` | `https://api.modelgate.ru` | Базовый LLM endpoint |
| `LLM_API_KEY` | — | API-ключ |
| `LLM_MODEL` | `deepseek-v4-flash` | Модель по умолчанию |
| `LLM_TEMPERATURE` | `0.7` | Температура LLM |
| `LLM_TIMEOUT` | `60s` | Таймаут попытки (все агенты) |
| `LLM_ENDPOINT_COORDINATOR` | `LLM_ENDPOINT` | Пер-агент endpoint |
| `LLM_ENDPOINT_ANALYST` | `LLM_ENDPOINT` | —//— |
| `LLM_ENDPOINT_CRITIC` | `LLM_ENDPOINT` | —//— |
| `LLM_API_KEY_COORDINATOR` | `LLM_API_KEY` | Пер-агент ключ |
| `LLM_API_KEY_ANALYST` | `LLM_API_KEY` | —//— |
| `LLM_API_KEY_CRITIC` | `LLM_API_KEY` | —//— |
| `LLM_MODEL_COORDINATOR` | `LLM_MODEL` | Модель для координатора (планирование) |
| `LLM_MODEL_ANALYST` | `LLM_MODEL` | Модель для аналитика (анализ + суммаризация) |
| `LLM_MODEL_CRITIC` | `LLM_MODEL` | Модель для критика (оценка) |
| `LLM_TIMEOUT_COORDINATOR` | `LLM_TIMEOUT` | Пер-агент таймаут |
| `LLM_TIMEOUT_ANALYST` | `LLM_TIMEOUT` | —//— |
| `LLM_TIMEOUT_CRITIC` | `LLM_TIMEOUT` | —//— |

**Эмбеддинги (chromem):**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `EMBEDDING_ENDPOINT` | `LLM_ENDPOINT/v1` | URL API эмбеддингов |
| `EMBEDDING_API_KEY` | `LLM_API_KEY` | API-ключ |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | Модель эмбеддингов |

**Поиск:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `OPENSERP_URL` | `http://localhost:8080` | URL OpenSERP |
| `SEARCH_ENGINE` | `duck` | Движок (duck/google/yandex/...) |
| `MAX_CONCURRENT_SEARCH` | `3` | Параллельных поисков |
| `MAX_PAGES_PER_QUERY` | `3` | Страниц на запрос |

**Кеш поиска:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `MIN_RELEVANT_FACTS` | `10` | Минимум релевантных фактов для пропуска поиска |
| `RECALL_TOP_K` | `15` | Сколько фактов возвращает векторный поиск |
| `RECALL_DISTANCE_THRESHOLD` | `0.5` | Порог средней косинусной дистанции (0–2) |
| `RECALL_LLM_CHECK` | `true` | LLM-проверка кеша перед пропуском |

**Исследование:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `MAX_ITERATIONS` | `3` | Итераций улучшения отчёта |
| `MAX_CONCURRENT_SUBTOPICS` | `3` | Параллельный анализ подтем |
| `MAX_SUBTOPIC_RETRIES` | `2` | Попыток дозапроса при неполном анализе |

**Хранилище:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `MEMORY_PATH` | `./data/malder_memory` | Путь к chromem (векторная память) |
| `SOURCE_STORE_PATH` | `{MEMORY_PATH}_sources.db` | Путь к bolt (provenance + отчёты) |

**Прочее:**

| Переменная | По умолчанию | Описание |
|-----------|-------------|---------|
| `SERVER_PORT` | `8080` | Порт HTTP-сервера |
| `MALDER_LOG_LEVEL` | `info` | Уровень (debug/info/warn/error) |
| `MALDER_DEBUG_FILE` | `malder_debug.log` | Файл для DEBUG-логов |

## Архитектура

Подробное описание архитектуры, компонентов и потока данных — в [docs/Architech.md](docs/Architech.md).

## Лицензия

MIT
