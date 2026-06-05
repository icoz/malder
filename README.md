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
| LLM_ENDPOINT | URL LLM API | http://localhost:11434/v1 |
| LLM_MODEL | Имя модели | llama3.2 |
| OPENSERP_URL | URL OpenSerp | http://localhost:8080 |
| MEMORY_PATH | Путь для сохранения памяти | ./data/malder_memory |
| MAX_CONCURRENT_SEARCH | Параллельных поисков | 3 |
| MAX_ITERATIONS | Итераций улучшения отчёта | 3 |
| SERVER_PORT | Порт сервера | 8080 |

## Лицензия
MIT
