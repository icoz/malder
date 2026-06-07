package main

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/icoz/malder/internal/agent"
	"github.com/icoz/malder/internal/llm"
	malderlog "github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/scheduler"
	"github.com/icoz/malder/internal/tool"
)

type Config struct {
	LLMEndpoint    string
	LLMAPIKey      string
	LLMModel       string
	LLMTemperature float64
	LLMTimeout     time.Duration

	LLMEndpointCoordinator string
	LLMAPIKeyCoordinator   string
	LLMModelCoordinator    string
	LLMTimeoutCoordinator  time.Duration

	LLMEndpointAnalyst string
	LLMAPIKeyAnalyst   string
	LLMModelAnalyst    string
	LLMTimeoutAnalyst  time.Duration

	LLMEndpointCritic string
	LLMAPIKeyCritic   string
	LLMModelCritic    string
	LLMTimeoutCritic  time.Duration

	EmbeddingEndpoint string
	EmbeddingAPIKey   string
	EmbeddingModel    string

	OpenSerpURL string

	MemoryPath      string
	SourceStorePath string

	MaxConcurrentSearch int
	MaxPagesPerQuery    int
	MinFactsForCache    int

	MaxIterations int

	MaxConcurrentSubtopics int
	MaxSubtopicRetries     int

	ServerPort string
}

func loadConfig() *Config {
	cfg := &Config{
		LLMEndpoint:         getEnv("LLM_ENDPOINT", "https://api.modelgate.ru"),
		LLMAPIKey:           getEnv("LLM_API_KEY", ""),
		LLMModel:            getEnv("LLM_MODEL", "deepseek-v4-flash"),
		LLMTemperature:      getEnvFloat("LLM_TEMPERATURE", 0.7),
		LLMTimeout:          getEnvDuration("LLM_TIMEOUT", 60*time.Second),
		OpenSerpURL:         getEnv("OPENSERP_URL", "http://localhost:8080"),
		MemoryPath:          getEnv("MEMORY_PATH", "./data/malder_memory"),
		SourceStorePath:     getEnv("SOURCE_STORE_PATH", ""),
		MaxConcurrentSearch: getEnvInt("MAX_CONCURRENT_SEARCH", 3),
		MaxPagesPerQuery:    getEnvInt("MAX_PAGES_PER_QUERY", 3),
		MinFactsForCache:    getEnvInt("MIN_FACTS_FOR_CACHE", 3),
		MaxIterations:          getEnvInt("MAX_ITERATIONS", 3),
		MaxConcurrentSubtopics: getEnvInt("MAX_CONCURRENT_SUBTOPICS", 3),
		MaxSubtopicRetries:     getEnvInt("MAX_SUBTOPIC_RETRIES", 2),
		ServerPort:             getEnv("SERVER_PORT", "8080"),
	}
	cfg.LLMEndpointCoordinator = getEnv("LLM_ENDPOINT_COORDINATOR", cfg.LLMEndpoint)
	cfg.LLMEndpointAnalyst = getEnv("LLM_ENDPOINT_ANALYST", cfg.LLMEndpoint)
	cfg.LLMEndpointCritic = getEnv("LLM_ENDPOINT_CRITIC", cfg.LLMEndpoint)
	cfg.LLMAPIKeyCoordinator = getEnv("LLM_API_KEY_COORDINATOR", cfg.LLMAPIKey)
	cfg.LLMAPIKeyAnalyst = getEnv("LLM_API_KEY_ANALYST", cfg.LLMAPIKey)
	cfg.LLMAPIKeyCritic = getEnv("LLM_API_KEY_CRITIC", cfg.LLMAPIKey)
	cfg.LLMModelCoordinator = getEnv("LLM_MODEL_COORDINATOR", cfg.LLMModel)
	cfg.LLMModelAnalyst = getEnv("LLM_MODEL_ANALYST", cfg.LLMModel)
	cfg.LLMModelCritic = getEnv("LLM_MODEL_CRITIC", cfg.LLMModel)
	cfg.LLMTimeoutCoordinator = getEnvDuration("LLM_TIMEOUT_COORDINATOR", cfg.LLMTimeout)
	cfg.LLMTimeoutAnalyst = getEnvDuration("LLM_TIMEOUT_ANALYST", cfg.LLMTimeout)
	cfg.LLMTimeoutCritic = getEnvDuration("LLM_TIMEOUT_CRITIC", cfg.LLMTimeout)
	cfg.EmbeddingEndpoint = getEnv("EMBEDDING_ENDPOINT", cfg.LLMEndpoint+"/v1")
	cfg.EmbeddingAPIKey = getEnv("EMBEDDING_API_KEY", cfg.LLMAPIKey)
	cfg.EmbeddingModel = getEnv("EMBEDDING_MODEL", "text-embedding-3-small")
	return cfg
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultVal
}

func main() {
	malderlog.Init()
	cfg := loadConfig()
	malderlog.Info("Запуск malder — LLM: %s, модель: %s, порт: %s, движок: %s, память: %s, эмбеддинги: %s/%s",
		cfg.LLMEndpoint, cfg.LLMModel, cfg.ServerPort, getEnv("SEARCH_ENGINE", "duck"), cfg.MemoryPath, cfg.EmbeddingEndpoint, cfg.EmbeddingModel)

	makeLLM := func(endpoint, apiKey string, timeout time.Duration) *llm.Client {
		return llm.NewClient(llm.Config{
			Endpoint: endpoint,
			APIKey:   apiKey,
			Timeout:  timeout,
		})
	}

	llmCoordinator := makeLLM(cfg.LLMEndpointCoordinator, cfg.LLMAPIKeyCoordinator, cfg.LLMTimeoutCoordinator)
	llmAnalyst := makeLLM(cfg.LLMEndpointAnalyst, cfg.LLMAPIKeyAnalyst, cfg.LLMTimeoutAnalyst)
	llmCritic := makeLLM(cfg.LLMEndpointCritic, cfg.LLMAPIKeyCritic, cfg.LLMTimeoutCritic)

	mem, err := memory.NewLongTermMemory(cfg.MemoryPath, cfg.EmbeddingEndpoint, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
	if err != nil {
		stdlog.Fatalf("Не удалось инициализировать память: %v", err)
	}
	defer mem.Close()

	sourceStorePath := cfg.SourceStorePath
	if sourceStorePath == "" {
		sourceStorePath = cfg.MemoryPath + "_sources.db"
	}
	sourceStore, err := memory.NewSourceStore(sourceStorePath)
	if err != nil {
		stdlog.Fatalf("Не удалось инициализировать SourceStore: %v", err)
	}
	defer sourceStore.Close()

	searchTool := tool.NewSearchTool(cfg.OpenSerpURL, 10*time.Second, getEnv("SEARCH_ENGINE", "duck"))
	fetchTool := tool.NewFetchPageTool(15 * time.Second)
	saveFactTool := tool.NewSaveFactTool(mem)
	schedCfg := scheduler.Config{
		InitialMax:      cfg.MaxConcurrentSearch,
		MinConcurrent:   1,
		MaxConcurrent:   8,
		TargetLatency:   2 * time.Second,
		WindowSize:      10,
		AdjustmentEvery: 30 * time.Second,
	}
	adaptiveScheduler := scheduler.NewAdaptiveScheduler(schedCfg)

	searchAgent := agent.NewSearchAgent(searchTool, fetchTool, mem, adaptiveScheduler, sourceStore, llmAnalyst, cfg.LLMModelAnalyst, cfg.MaxPagesPerQuery, cfg.MinFactsForCache)

	analystAgent := agent.NewAnalystAgent(llmAnalyst, cfg.LLMModelAnalyst, cfg.LLMTemperature, mem, saveFactTool, sourceStore)

	criticAgent := agent.NewCriticAgent(llmCritic, cfg.LLMModelCritic, cfg.LLMTemperature)

	coordinator := agent.NewCoordinator(agent.CoordinatorConfig{
		LLM:                    llmCoordinator,
		Model:                  cfg.LLMModelCoordinator,
		Temperature:            cfg.LLMTemperature,
		Memory:                 mem,
		SourceStore:            sourceStore,
		SearchAgent:            searchAgent,
		AnalystAgent:           analystAgent,
		CriticAgent:            criticAgent,
		MaxIterations:          cfg.MaxIterations,
		MaxConcurrentSubtopics: cfg.MaxConcurrentSubtopics,
		MaxSubtopicRetries:     cfg.MaxSubtopicRetries,
	})

	http.HandleFunc("/research", researchHandler(coordinator))
	http.HandleFunc("/research/stream", sseResearchHandler(coordinator))
	http.HandleFunc("/health", healthHandler)

	addr := ":" + cfg.ServerPort
	malderlog.Info("Сервер запущен на %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		stdlog.Fatal(err)
	}
}

type researchRequest struct {
	Query string `json:"query"`
}

type researchResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

func researchHandler(coord *agent.CoordinatorAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		var req researchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Query == "" {
			malderlog.Warn("Запрос research: пустой query")
			writeJSONError(w, "Query cannot be empty", http.StatusBadRequest)
			return
		}

		malderlog.Info("Запрос research: query=%q", req.Query)
		result, err := coord.Run(r.Context(), req.Query)
		if err != nil {
			malderlog.Warn("Запрос research: ошибка=%v", err)
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		malderlog.Info("Запрос research: готово, длина=%d", len(result))
		writeJSON(w, researchResponse{Result: result})
	}
}

func sseResearchHandler(coord *agent.CoordinatorAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		query := r.URL.Query().Get("q")
		if query == "" {
			malderlog.Warn("Запрос research/stream: пустой query")
			fmt.Fprintf(w, "event: error\ndata: missing query parameter 'q'\n\n")
			flusher.Flush()
			return
		}

		malderlog.Info("Запрос research/stream: query=%q", query)
		resultChan := make(chan struct {
			result string
			err    error
		}, 1)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		go func() {
			defer malderlog.Recover("SSE research handler")
			reporter := func(event string, data map[string]any) {
				dataJSON, _ := json.Marshal(data)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(dataJSON))
				flusher.Flush()
			}
			tempCoord := agent.NewCoordinator(agent.CoordinatorConfig{
				LLM:                    coord.LLM(),
				Model:                  coord.Model(),
				Temperature:            coord.Temperature(),
				Memory:                 coord.Memory(),
				SourceStore:            nil,
				SearchAgent:            coord.SearchAgent(),
				AnalystAgent:           coord.AnalystAgent(),
				CriticAgent:            coord.CriticAgent(),
				MaxIterations:          coord.MaxIterations(),
				MaxConcurrentSubtopics: coord.MaxConcurrentSubtopics(),
				MaxSubtopicRetries:     coord.MaxSubtopicRetries(),
			})
			tempCoord.SetProgressReporter(reporter)
			result, err := tempCoord.Run(ctx, query)
			resultChan <- struct {
				result string
				err    error
			}{result, err}
		}()

		select {
		case <-ctx.Done():
			fmt.Fprintf(w, "event: cancelled\ndata: {}\n\n")
			flusher.Flush()
		case res := <-resultChan:
			if res.err != nil {
				malderlog.Warn("Запрос research/stream: ошибка=%v", res.err)
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(res.err.Error()))
			} else {
				malderlog.Info("Запрос research/stream: готово, длина=%d", len(res.result))
				resultData, _ := json.Marshal(map[string]string{"result": res.result})
				fmt.Fprintf(w, "event: result\ndata: %s\n\n", resultData)
			}
			flusher.Flush()
		}
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(researchResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, resp researchResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
