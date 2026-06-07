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

	LLMModelCoordinator string
	LLMModelAnalyst     string
	LLMModelCritic      string

	OpenSerpURL string

	MemoryPath string

	MaxConcurrentSearch int
	MaxPagesPerQuery    int

	MaxIterations int

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
		MaxConcurrentSearch: getEnvInt("MAX_CONCURRENT_SEARCH", 3),
		MaxPagesPerQuery:    getEnvInt("MAX_PAGES_PER_QUERY", 3),
		MaxIterations:       getEnvInt("MAX_ITERATIONS", 3),
		ServerPort:          getEnv("SERVER_PORT", "8080"),
	}
	cfg.LLMModelCoordinator = getEnv("LLM_MODEL_COORDINATOR", cfg.LLMModel)
	cfg.LLMModelAnalyst = getEnv("LLM_MODEL_ANALYST", cfg.LLMModel)
	cfg.LLMModelCritic = getEnv("LLM_MODEL_CRITIC", cfg.LLMModel)
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
	malderlog.Info("Запуск malder с конфигурацией: %+v", cfg)

	llmClient := llm.NewClient(llm.Config{
		Endpoint: cfg.LLMEndpoint,
		APIKey:   cfg.LLMAPIKey,
		Timeout:  cfg.LLMTimeout,
	})

	mem, err := memory.NewLongTermMemory(cfg.MemoryPath)
	if err != nil {
		stdlog.Fatalf("Не удалось инициализировать память: %v", err)
	}
	defer mem.Close()

	searchTool := tool.NewSearchTool(cfg.OpenSerpURL, 10*time.Second, "yandex")
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

	searchAgent := agent.NewSearchAgent(searchTool, fetchTool, mem, adaptiveScheduler, cfg.MaxPagesPerQuery)

	analystAgent := agent.NewAnalystAgent(llmClient, cfg.LLMModelAnalyst, cfg.LLMTemperature, mem, saveFactTool)

	criticAgent := agent.NewCriticAgent(llmClient, cfg.LLMModelCritic, cfg.LLMTemperature)

	coordinator := agent.NewCoordinator(agent.CoordinatorConfig{
		LLM:           llmClient,
		Model:         cfg.LLMModelCoordinator,
		Temperature:   cfg.LLMTemperature,
		Memory:        mem,
		SearchAgent:   searchAgent,
		AnalystAgent:  analystAgent,
		CriticAgent:   criticAgent,
		MaxIterations: cfg.MaxIterations,
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
			writeJSONError(w, "Query cannot be empty", http.StatusBadRequest)
			return
		}

		result, err := coord.Run(r.Context(), req.Query)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
			fmt.Fprintf(w, "event: error\ndata: missing query parameter 'q'\n\n")
			flusher.Flush()
			return
		}

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
				LLM:           coord.LLM(),
				Model:         coord.Model(),
				Temperature:   coord.Temperature(),
				Memory:        coord.Memory(),
				SearchAgent:   coord.SearchAgent(),
				AnalystAgent:  coord.AnalystAgent(),
				CriticAgent:   coord.CriticAgent(),
				MaxIterations: coord.MaxIterations(),
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
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(res.err.Error()))
			} else {
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
