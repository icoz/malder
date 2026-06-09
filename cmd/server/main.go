package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	stdlog "log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/icoz/malder/internal/agent"
	"github.com/icoz/malder/internal/llm"
	malderlog "github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
	"github.com/icoz/malder/internal/scheduler"
	"github.com/icoz/malder/internal/tool"
	"github.com/yuin/goldmark"
	"go.etcd.io/bbolt"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

var mdRenderer = goldmark.New()

type logResponseWriter struct {
	http.ResponseWriter
	code int
}

func (w *logResponseWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func logPageHandler(next http.HandlerFunc, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lrw := &logResponseWriter{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		malderlog.Debug("→ WebUI.%s", name)
		next(lrw, r)
		malderlog.Debug("← WebUI.%s: %s %s → %d (%v)", name, r.Method, r.URL.RequestURI(), lrw.code, time.Since(start))
	}
}

func russianDate(ts int64) string {
	t := time.Unix(0, ts)
	months := []string{"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря"}
	return fmt.Sprintf("%d %s %d, %02d:%02d",
		t.Day(), months[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

func russianDatePtr(ts *int64) string {
	if ts == nil {
		return ""
	}
	return russianDate(*ts)
}

func statusLabel(s memory.ReportStatus) template.HTML {
	var cls string
	switch s {
	case memory.ReportStatusCompleted:
		cls = "status-completed"
		return template.HTML(fmt.Sprintf(`<span class="status-badge %s">Готов</span>`, cls))
	case memory.ReportStatusInProgress:
		cls = "status-in_progress"
		return template.HTML(fmt.Sprintf(`<span class="status-badge %s">Выполняется</span>`, cls))
	case memory.ReportStatusError:
		cls = "status-error"
		return template.HTML(fmt.Sprintf(`<span class="status-badge %s">Ошибка</span>`, cls))
	default:
		return template.HTML(s)
	}
}

func durationLabel(ms int64) string {
	if ms == 0 {
		return "—"
	}
	d := time.Duration(ms) * time.Millisecond
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%d мин %d сек", m, s)
	}
	return fmt.Sprintf("%d сек", s)
}

func contains(slice any, s string) bool {
	switch v := slice.(type) {
	case []string:
		for _, item := range v {
			if item == s {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if vs, ok := item.(string); ok && vs == s {
				return true
			}
		}
	}
	return false
}

type Config struct {
	LLMEndpoint    string
	LLMAPIKey      string
	LLMModel       string
	LLMTemperature float64
	LLMTimeout     time.Duration
	LLMRetryAttempts int
	LLMRetryBaseDelay time.Duration

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

	EmbeddingEndpoint      string
	EmbeddingAPIKey        string
	EmbeddingModel         string
	EmbeddingRetryAttempts int
	EmbeddingRetryBaseDelay time.Duration

	VLMEndpoint string
	VLMAPIKey   string
	VLMModel    string
	VLMTimeout  time.Duration

	OpenSerpURL string

	MemoryPath      string
	SourceStorePath string
	KnowledgePath   string

	MaxConcurrentSearch int
	MaxPagesPerQuery    int
	MinRelevantFacts    int

	RecallTopK           int
	RecallDistThreshold  float64
	RecallLLMCheck       bool

	MaxIterations int

	MaxConcurrentSubtopics int
	MaxSubtopicRetries     int

	Verbosity string

	ServerPort string
}

func loadConfig() *Config {
	cfg := &Config{
		LLMEndpoint:         getEnv("LLM_ENDPOINT", "https://api.modelgate.ru"),
		LLMAPIKey:           getEnv("LLM_API_KEY", ""),
		LLMModel:            getEnv("LLM_MODEL", "deepseek-v4-flash"),
		LLMTemperature:      getEnvFloat("LLM_TEMPERATURE", 0.7),
		LLMTimeout:          getEnvDuration("LLM_TIMEOUT", 60*time.Second),
		LLMRetryAttempts:    getEnvInt("LLM_RETRY_ATTEMPTS", 3),
		LLMRetryBaseDelay:   getEnvDuration("LLM_RETRY_BASE_DELAY", 1*time.Second),
		OpenSerpURL:         getEnv("OPENSERP_URL", "http://localhost:8080"),
		MemoryPath:          getEnv("MEMORY_PATH", "./data/malder_memory"),
		SourceStorePath:     getEnv("SOURCE_STORE_PATH", ""),
		KnowledgePath:       getEnv("KNOWLEDGE_PATH", "./data/malder_knowledge"),
		MaxConcurrentSearch: getEnvInt("MAX_CONCURRENT_SEARCH", 3),
		MaxPagesPerQuery:    getEnvInt("MAX_PAGES_PER_QUERY", 3),
		MinRelevantFacts:    getEnvInt("MIN_RELEVANT_FACTS", getEnvInt("MIN_FACTS_FOR_CACHE", 10)),
		RecallTopK:          getEnvInt("RECALL_TOP_K", 15),
		RecallDistThreshold: getEnvFloat("RECALL_DISTANCE_THRESHOLD", 0.5),
		RecallLLMCheck:      os.Getenv("RECALL_LLM_CHECK") != "false",
		MaxIterations:          getEnvInt("MAX_ITERATIONS", 3),
		MaxConcurrentSubtopics: getEnvInt("MAX_CONCURRENT_SUBTOPICS", 3),
		MaxSubtopicRetries:     getEnvInt("MAX_SUBTOPIC_RETRIES", 2),
		Verbosity:              getEnv("VERBOSITY", "normal"),
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
	cfg.EmbeddingRetryAttempts = getEnvInt("EMBEDDING_RETRY_ATTEMPTS", cfg.LLMRetryAttempts)
	cfg.EmbeddingRetryBaseDelay = getEnvDuration("EMBEDDING_RETRY_BASE_DELAY", cfg.LLMRetryBaseDelay)
	cfg.VLMEndpoint = getEnv("VLM_ENDPOINT", cfg.LLMEndpoint)
	cfg.VLMAPIKey = getEnv("VLM_API_KEY", cfg.LLMAPIKey)
	cfg.VLMModel = getEnv("VLM_MODEL", "ibm-granite-vision-7b")
	cfg.VLMTimeout = getEnvDuration("VLM_TIMEOUT", 30*time.Second)
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
			Endpoint:         endpoint,
			APIKey:           apiKey,
			Timeout:          timeout,
			RetryMaxAttempts: cfg.LLMRetryAttempts,
			RetryBaseDelay:   cfg.LLMRetryBaseDelay,
		})
	}

	llmCoordinator := makeLLM(cfg.LLMEndpointCoordinator, cfg.LLMAPIKeyCoordinator, cfg.LLMTimeoutCoordinator)
	llmAnalyst := makeLLM(cfg.LLMEndpointAnalyst, cfg.LLMAPIKeyAnalyst, cfg.LLMTimeoutAnalyst)
	llmCritic := makeLLM(cfg.LLMEndpointCritic, cfg.LLMAPIKeyCritic, cfg.LLMTimeoutCritic)

	mem, err := memory.NewLongTermMemory(cfg.MemoryPath, cfg.EmbeddingEndpoint, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.RecallTopK, &memory.RetryConfig{
		MaxAttempts: cfg.EmbeddingRetryAttempts,
		BaseDelay:   cfg.EmbeddingRetryBaseDelay,
	})
	if err != nil {
		stdlog.Fatalf("Не удалось инициализировать память: %v", err)
	}
	defer mem.Close()

	sourceStorePath := cfg.SourceStorePath
	if sourceStorePath == "" {
		sourceStorePath = cfg.MemoryPath + "_sources.db"
	}
	boltDB, err := bbolt.Open(sourceStorePath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		stdlog.Fatalf("Не удалось открыть bolt DB: %v", err)
	}
	defer boltDB.Close()

	sourceStore := memory.NewSourceStore(boltDB)
	reportStore := memory.NewReportStore(boltDB)

	if n, err := reportStore.FailInProgressReports(context.Background(), "сервер был перезапущен"); err != nil {
		malderlog.Warn("Очистка зависших репортов: ошибка: %v", err)
	} else if n > 0 {
		malderlog.Info("Очистка зависших репортов: %d помечено как error", n)
	}

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

	verbosity := agent.ParseVerbosity(cfg.Verbosity)

	searchAgent := agent.NewSearchAgent(searchTool, fetchTool, mem, adaptiveScheduler, sourceStore, llmAnalyst, cfg.LLMModelAnalyst, cfg.MaxPagesPerQuery, cfg.MinRelevantFacts, cfg.RecallDistThreshold, cfg.RecallLLMCheck, verbosity)

	analystAgent := agent.NewAnalystAgent(llmAnalyst, cfg.LLMModelAnalyst, cfg.LLMTemperature, mem, saveFactTool, sourceStore, verbosity)

	criticAgent := agent.NewCriticAgent(llmCritic, cfg.LLMModelCritic, cfg.LLMTemperature, verbosity)

	coordinator := agent.NewCoordinator(agent.CoordinatorConfig{
		LLM:                    llmCoordinator,
		Model:                  cfg.LLMModelCoordinator,
		Temperature:            cfg.LLMTemperature,
		Memory:                 mem,
		SourceStore:            sourceStore,
		SearchAgent:            searchAgent,
		AnalystAgent:           analystAgent,
		CriticAgent:            criticAgent,
		Verbosity:              verbosity,
		MaxIterations:          cfg.MaxIterations,
		MaxConcurrentSubtopics: cfg.MaxConcurrentSubtopics,
		MaxSubtopicRetries:     cfg.MaxSubtopicRetries,
	})

	funcMap := template.FuncMap{
		"russianDate":    russianDate,
		"russianDatePtr": russianDatePtr,
		"statusLabel":    statusLabel,
		"durationLabel":  durationLabel,
		"contains":       contains,
	}

	base := template.New("").Funcs(funcMap)
	template.Must(base.ParseFS(templateFS, "web/templates/base.html"))

	indexTpl := template.Must(base.Clone())
	template.Must(indexTpl.ParseFS(templateFS, "web/templates/index.html"))

	listTpl := template.Must(base.Clone())
	template.Must(listTpl.ParseFS(templateFS, "web/templates/report_list.html"))

	detailTpl := template.Must(base.Clone())
	template.Must(detailTpl.ParseFS(templateFS, "web/templates/report_detail.html"))

	notFoundTpl := template.Must(base.Clone())
	template.Must(notFoundTpl.ParseFS(templateFS, "web/templates/not_found.html"))

	errorTpl := template.Must(base.Clone())
	template.Must(errorTpl.ParseFS(templateFS, "web/templates/error.html"))

	staticContent, _ := fs.Sub(staticFS, "web/static")
	staticHandler := http.FileServer(http.FS(staticContent))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", logPageHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			indexHandler(indexTpl)(w, r)
		} else {
			notFoundHandler(notFoundTpl)(w, r)
		}
	}, "indexHandler"))
	mux.HandleFunc("GET /reports", logPageHandler(reportListHandler(listTpl, reportStore), "reportListHandler"))
	mux.HandleFunc("GET /reports/{id}", logPageHandler(reportDetailHandler(detailTpl, notFoundTpl, reportStore), "reportDetailHandler"))
	mux.HandleFunc("GET /api/reports", apiReportListHandler(reportStore))
	mux.HandleFunc("GET /api/reports/{id}", apiReportGetHandler(reportStore))
	mux.HandleFunc("GET /api/reports/{id}/raw", apiReportRawHandler(reportStore))
	mux.HandleFunc("POST /api/research", apiResearchHandler(coordinator, reportStore))
	mux.HandleFunc("GET /api/research/stream", apiSSEResearchHandler(coordinator, reportStore))
	mux.HandleFunc("POST /api/reports/{id}/fail", apiReportFailHandler(reportStore))
	mux.HandleFunc("POST /api/reports/{id}/retry", apiReportRetryHandler(coordinator, reportStore))
	mux.HandleFunc("POST /api/reports/{id}/resume", apiReportResumeHandler(coordinator, reportStore))
	mux.HandleFunc("GET /api/health", healthHandler)
	mux.Handle("GET /static/", cacheControlMiddleware(
		http.StripPrefix("/static/", staticHandler),
	))

	addr := ":" + cfg.ServerPort
	malderlog.Info("Сервер запущен на %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		stdlog.Fatal(err)
	}
}

func indexHandler(tmpls *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpls.ExecuteTemplate(w, "index.html", nil)
	}
}

func reportListHandler(tmpls *template.Template, store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reports, err := store.List()
		if err != nil {
			malderlog.Warn("Список отчётов: ошибка: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpls.ExecuteTemplate(w, "report_list.html", map[string]any{
			"Reports": reports,
		})
	}
}

func reportDetailHandler(detailTpl, notFoundTpl *template.Template, store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		report, err := store.Get(id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			notFoundTpl.ExecuteTemplate(w, "not_found.html", nil)
			return
		}
		var htmlContent template.HTML
		if report.ReportText != "" {
			var buf bytes.Buffer
			if err := mdRenderer.Convert([]byte(report.ReportText), &buf); err == nil {
				htmlContent = template.HTML(buf.String())
			}
		}
		var execSummaryHTML template.HTML
		if report.ExecutiveSummary != "" {
			var buf bytes.Buffer
			if err := mdRenderer.Convert([]byte(report.ExecutiveSummary), &buf); err == nil {
				execSummaryHTML = template.HTML(buf.String())
			}
		}
		wordCount := 0
		if report.ReportText != "" {
			words := strings.Fields(report.ReportText)
			wordCount = len(words)
		}
		var progressMap map[string]any
		if report.RawProgress != "" {
			json.Unmarshal([]byte(report.RawProgress), &progressMap)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		detailTpl.ExecuteTemplate(w, "report_detail.html", map[string]any{
			"Report":           report,
			"ReportHTML":       htmlContent,
			"ExecSummaryHTML":  execSummaryHTML,
			"WordCount":        wordCount,
			"Progress":         progressMap,
			"HasCheckpoint":    report.CheckpointJSON != "",
		})
	}
}

func apiReportListHandler(store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reports, err := store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, reports)
	}
}

func apiReportGetHandler(store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		report, err := store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func apiReportRawHandler(store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		report, err := store.Get(id)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(report.ReportText))
	}
}

type apiResearchRequest struct {
	Query string `json:"query"`
}

func apiResearchHandler(coord *agent.CoordinatorAgent, store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req apiResearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
			return
		}
		if req.Query == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Query cannot be empty"})
			return
		}

		reportID, err := store.Create(req.Query)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		coord.SetProgressSaver(func(event string, data map[string]any) {
			store.SaveProgress(reportID, event, data)
		})
		coord.SetCheckpointSaver(func(cpJSON string) {
			store.SaveCheckpoint(reportID, cpJSON)
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-r.Context().Done():
				cancel()
			case <-ctx.Done():
			}
		}()

		start := time.Now()
		result, err := coord.Run(ctx, req.Query)
		duration := time.Since(start)

		if err != nil {
			store.Fail(reportID, err.Error(), duration)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		store.Complete(reportID, result.Report, result.ExecutiveSummary, result.SourceURLs, duration)
		writeJSON(w, http.StatusOK, map[string]any{
			"report_id":         reportID,
			"report":            result.Report,
			"executive_summary": result.ExecutiveSummary,
			"source_urls":       result.SourceURLs,
			"duration_ms":       duration.Milliseconds(),
		})
	}
}

func apiSSEResearchHandler(coord *agent.CoordinatorAgent, store *memory.ReportStore) http.HandlerFunc {
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

		depth := r.URL.Query().Get("v")

		reportID, err := store.Create(query)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape("Failed to create report: "+err.Error()))
			flusher.Flush()
			return
		}

		fmt.Fprintf(w, "event: started\ndata: {\"report_id\":\"%s\"}\n\n", reportID)
		flusher.Flush()

		malderlog.Info("Запрос research/stream: query=%q, report_id=%s", query, reportID)

		resultChan := make(chan struct {
			result *agent.ResearchResult
			err    error
		}, 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			select {
			case <-r.Context().Done():
				cancel()
			case <-ctx.Done():
			}
		}()

		go func() {
			defer malderlog.Recover("SSE research handler")
			reporter := func(event string, data map[string]any) {
				defer func() { recover() }()
				if ctx.Err() != nil {
					return
				}
				malderlog.Debug("→ WebUI.SSE(event=%s, report_id=%s)", event, reportID)
				dataJSON, _ := json.Marshal(data)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(dataJSON))
				flusher.Flush()
			}
			tempCoord := agent.NewCoordinator(agent.CoordinatorConfig{
				LLM:                    coord.LLM(),
				Model:                  coord.Model(),
				Temperature:            coord.Temperature(),
				Memory:                 coord.Memory(),
				SourceStore:            coord.SourceStore(),
				SearchAgent:            coord.SearchAgent(),
				AnalystAgent:           coord.AnalystAgent(),
				CriticAgent:            coord.CriticAgent(),
				Verbosity:              agent.ParseVerbosity(depth),
				MaxIterations:          coord.MaxIterations(),
				MaxConcurrentSubtopics: coord.MaxConcurrentSubtopics(),
				MaxSubtopicRetries:     coord.MaxSubtopicRetries(),
			})
			tempCoord.SetProgressReporter(reporter)
			tempCoord.SetProgressSaver(func(event string, data map[string]any) {
				store.SaveProgress(reportID, event, data)
			})
			tempCoord.SetCheckpointSaver(func(cpJSON string) {
				store.SaveCheckpoint(reportID, cpJSON)
			})
			result, err := tempCoord.Run(ctx, query)
			resultChan <- struct {
				result *agent.ResearchResult
				err    error
			}{result, err}
		}()

		select {
		case <-ctx.Done():
			store.Fail(reportID, "cancelled", time.Since(time.Now()))
			fmt.Fprintf(w, "event: cancelled\ndata: {}\n\n")
			flusher.Flush()
		case res := <-resultChan:
			if res.err != nil {
				store.Fail(reportID, res.err.Error(), time.Since(time.Now()))
				malderlog.Warn("Запрос research/stream: ошибка=%v", res.err)
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(res.err.Error()))
			} else {
				store.Complete(reportID, res.result.Report, res.result.ExecutiveSummary, res.result.SourceURLs, time.Since(time.Now()))
				malderlog.Info("Запрос research/stream: готово, report_id=%s", reportID)
				resultData, _ := json.Marshal(map[string]string{"report_id": reportID})
				fmt.Fprintf(w, "event: result\ndata: %s\n\n", resultData)
			}
			flusher.Flush()
		}
	}
}

func apiReportFailHandler(store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		report, err := store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if report.Status != memory.ReportStatusInProgress {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "report is not in progress"})
			return
		}

		var body struct {
			Error string `json:"error"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Error == "" {
			body.Error = "завершён вручную"
		}

		if err := store.Fail(id, body.Error, 0); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func apiReportRetryHandler(coord *agent.CoordinatorAgent, store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		report, err := store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if report.Status == memory.ReportStatusCompleted {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "report is already completed"})
			return
		}

		newID, err := store.Create(report.Query)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		start := time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-r.Context().Done():
				cancel()
			case <-ctx.Done():
			}
		}()
		result, err := coord.Run(ctx, report.Query)
		duration := time.Since(start)

		if err != nil {
			store.Fail(newID, err.Error(), duration)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		store.Complete(newID, result.Report, result.ExecutiveSummary, result.SourceURLs, duration)
		writeJSON(w, http.StatusOK, map[string]any{
			"report_id":         newID,
			"old_report_id":     id,
			"report":            result.Report,
			"executive_summary": result.ExecutiveSummary,
			"source_urls":       result.SourceURLs,
			"duration_ms":       duration.Milliseconds(),
		})
	}
}

func apiReportResumeHandler(coord *agent.CoordinatorAgent, store *memory.ReportStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		report, err := store.Get(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if report.Status != memory.ReportStatusError {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "report must be in error status"})
			return
		}
		if report.CheckpointJSON == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no checkpoint available for this report"})
			return
		}

		var cp agent.Checkpoint
		if err := json.Unmarshal([]byte(report.CheckpointJSON), &cp); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid checkpoint: " + err.Error()})
			return
		}

		tempCoord := agent.NewCoordinator(agent.CoordinatorConfig{
			LLM:                    coord.LLM(),
			Model:                  coord.Model(),
			Temperature:            coord.Temperature(),
			Memory:                 coord.Memory(),
			SourceStore:            coord.SourceStore(),
			SearchAgent:            coord.SearchAgent(),
			AnalystAgent:           coord.AnalystAgent(),
			CriticAgent:            coord.CriticAgent(),
			Verbosity:              coord.Verbosity(),
			MaxIterations:          coord.MaxIterations(),
			MaxConcurrentSubtopics: coord.MaxConcurrentSubtopics(),
			MaxSubtopicRetries:     coord.MaxSubtopicRetries(),
		})
		tempCoord.SetProgressSaver(func(event string, data map[string]any) {
			store.SaveProgress(id, event, data)
		})
		tempCoord.SetCheckpointSaver(func(cpJSON string) {
			store.SaveCheckpoint(id, cpJSON)
		})

		store.ResetToInProgress(id)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-r.Context().Done():
				cancel()
			case <-ctx.Done():
			}
		}()

		start := time.Now()
		result, err := tempCoord.RunWithCheckpoint(ctx, report.Query, &cp)
		duration := time.Since(start)

		if err != nil {
			store.Fail(id, err.Error(), duration)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		store.Complete(id, result.Report, result.ExecutiveSummary, result.SourceURLs, duration)
		writeJSON(w, http.StatusOK, map[string]any{
			"report_id":         id,
			"report":            result.Report,
			"executive_summary": result.ExecutiveSummary,
			"source_urls":       result.SourceURLs,
			"duration_ms":       duration.Milliseconds(),
			"resumed":           true,
		})
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func notFoundHandler(tmpls *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		tmpls.ExecuteTemplate(w, "not_found.html", nil)
	}
}

func errorHandler(tmpls *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		tmpls.ExecuteTemplate(w, "error.html", map[string]any{
			"Error": "Произошла внутренняя ошибка сервера.",
		})
	}
}

func cacheControlMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
