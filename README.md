# malder – My Local Deep Research (MLDR)

Мультиагентная система для глубокого исследования тем с использованием LLM и поиска через Яндекс.

## Запуск

### Требования
- Docker и Docker Compose
- Запущенная LLM (локально, например Ollama) или доступ к OpenAI API

### Быстрый старт

1. Клонируйте репозиторий
2. Настройте переменные окружения в файле `.env` (см. пример)
3. Запустите:
   ```bash
   docker-compose up --build
   ```

### API

- `POST /research` – синхронное исследование (JSON)
  ```bash
  curl -X POST http://localhost:8080/research -H "Content-Type: application/json" -d '{"query":"что такое горутины"}'
  ```
- `GET /research/stream?q=...` – исследование с SSE-прогрессом
- `GET /health` – проверка статуса

### Конфигурация через переменные окружения

| Переменная | Описание | По умолчанию |
|------------|----------|--------------|
| LLM_ENDPOINT | URL LLM API | https://api.modelgate.ru |
| LLM_API_KEY | API ключ | "" |
| LLM_MODEL | Модель по умолчанию | deepseek-v4-flash |
| LLM_MODEL_COORDINATOR | Модель для координатора (планирование) | LLM_MODEL |
| LLM_MODEL_ANALYST | Модель для аналитика (генерация отчёта) | LLM_MODEL |
| LLM_MODEL_CRITIC | Модель для критика (оценка) | LLM_MODEL |
| EMBEDDING_ENDPOINT | URL API эмбеддингов | LLM_ENDPOINT/v1 |
| EMBEDDING_API_KEY | API ключ для эмбеддингов | LLM_API_KEY |
| EMBEDDING_MODEL | Модель эмбеддингов | text-embedding-3-small |
| SEARCH_ENGINE | Поисковый движок (duck/google/yandex/...) | duck |
| MALDER_LOG_LEVEL | Уровень журналирования (debug/info/warn/error) | info |
| MALDER_DEBUG_FILE | Файл для DEBUG-логов | malder_debug.log |
| LLM_TEMPERATURE | Температура LLM | 0.7 |
| LLM_TIMEOUT | Таймаут запроса к LLM (по умолчанию для всех агентов) | 60s |
| LLM_TIMEOUT_COORDINATOR | Таймаут для координатора | LLM_TIMEOUT |
| LLM_TIMEOUT_ANALYST | Таймаут для аналитика | LLM_TIMEOUT |
| LLM_TIMEOUT_CRITIC | Таймаут для критика | LLM_TIMEOUT |
| OPENSERP_URL | URL OpenSerp | http://localhost:8080 |
| MEMORY_PATH | Путь для сохранения памяти | ./data/malder_memory |
| MAX_CONCURRENT_SEARCH | Параллельных поисков | 3 |
| MIN_RELEVANT_FACTS | Минимум релевантных фактов для пропуска поиска | 10 |
| RECALL_TOP_K | Сколько фактов возвращает векторный поиск | 15 |
| RECALL_DISTANCE_THRESHOLD | Максимальная средняя косинусная дистанция (0–2) | 0.5 |
| RECALL_LLM_CHECK | Проверять ли кеш через LLM перед пропуском поиска | true |
| MAX_ITERATIONS | Итераций улучшения отчёта | 3 |
| SERVER_PORT | Порт сервера | 8080 |

## Лицензия
MIT
