package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/icoz/malder/internal/llm"
	malderlog "github.com/icoz/malder/internal/log"
	"github.com/icoz/malder/internal/memory"
)

var imageRefRE = regexp.MustCompile(`!\[.*?\]\((media/.*?)\)`)

type DocumentAgent struct {
	vlm        *llm.Client
	vlmModel   string
	memory     *memory.LongTermMemory
	kbStore    *memory.KnowledgeStore
	pandocPath string
}

func NewDocumentAgent(vlm *llm.Client, vlmModel string, mem *memory.LongTermMemory, kbStore *memory.KnowledgeStore) *DocumentAgent {
	return &DocumentAgent{
		vlm:        vlm,
		vlmModel:   vlmModel,
		memory:     mem,
		kbStore:    kbStore,
		pandocPath: "pandoc",
	}
}

func (d *DocumentAgent) Process(ctx context.Context, filePath, originalName string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "malder-doc-*")
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	mdPath := filepath.Join(tmpDir, "output.md")
	pandocArgs := []string{filePath, "-o", mdPath, "--wrap=preserve", "--extract-media=" + tmpDir}
	cmd := exec.CommandContext(ctx, d.pandocPath, pandocArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pandoc: %w\nstderr: %s", err, stderr.String())
	}

	rawMD, err := os.ReadFile(mdPath)
	if err != nil {
		return "", fmt.Errorf("read pandoc output: %w", err)
	}
	markdown := string(rawMD)

	markdown, imageCount, err := d.describeImages(ctx, tmpDir, markdown)
	if err != nil {
		malderlog.Warn("DocumentAgent: ошибка VLM-описания изображений: %v", err)
	}

	chunks := chunkMarkdown(markdown)
	meta := &memory.DocumentMeta{
		OriginalName: originalName,
		ContentType:  detectContentType(originalName),
		Size:         int64(len(rawMD)),
		ChunkCount:   len(chunks),
	}

	docID, err := d.kbStore.Create(meta, markdown)
	if err != nil {
		return "", fmt.Errorf("kb store create: %w", err)
	}

	chunkIDs := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("kb:%s:%04d", docID, i)
		if err := d.memory.SaveKnowledgeChunk(ctx, chunkID, chunk); err != nil {
			malderlog.Warn("DocumentAgent: ошибка сохранения чанка %s: %v", chunkID, err)
			continue
		}
		chunkIDs = append(chunkIDs, chunkID)
	}

	if err := d.kbStore.SaveChunkIDs(docID, chunkIDs); err != nil {
		malderlog.Warn("DocumentAgent: ошибка сохранения chunkIDs: %v", err)
	}

	malderlog.Info("DocumentAgent: обработан %s → %s, изображений=%d, чанков=%d", originalName, docID, imageCount, len(chunks))
	return docID, nil
}

func (d *DocumentAgent) describeImages(ctx context.Context, tmpDir, markdown string) (string, int, error) {
	matches := imageRefRE.FindAllStringSubmatch(markdown, -1)
	if len(matches) == 0 {
		return markdown, 0, nil
	}

	type imgJob struct {
		ref   string
		path  string
		index int
	}
	var jobs []imgJob
	for _, m := range matches {
		ref := m[1]
		absPath := filepath.Join(tmpDir, ref)
		jobs = append(jobs, imgJob{ref: m[0], path: absPath, index: len(jobs)})
	}

	descriptions := make([]string, len(jobs))
	for i, job := range jobs {
		desc, err := d.describeOneImage(ctx, job.path)
		if err != nil {
			malderlog.Warn("DocumentAgent: VLM ошибка для %s: %v", job.path, err)
			descriptions[i] = fmt.Sprintf("[Изображение: %s]", filepath.Base(job.path))
		} else {
			descriptions[i] = desc
		}
	}

	for i, job := range jobs {
		replacement := fmt.Sprintf("\n> 📊 **Описание схемы/графика:** %s\n", descriptions[i])
		markdown = strings.Replace(markdown, job.ref, replacement, 1)
	}

	return markdown, len(jobs), nil
}

func (d *DocumentAgent) describeOneImage(ctx context.Context, path string) (string, error) {
	imgData, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	if !shouldProcessImage(imgData) {
		return "", fmt.Errorf("изображение пропущено эвристикой")
	}

	b64 := base64.StdEncoding.EncodeToString(imgData)
	prompt := "Опиши эту схему, диаграмму или график текстом. " +
		"Перечисли все ключевые элементы, подписи осей, числовые значения. " +
		"Не используй маркдаун, пиши связным текстом."
	systemPrompt := "Ты — ассистент, который описывает визуальные материалы из документов " +
		"для текстовой базы знаний. Отвечай только текстовым описанием."

	desc, err := d.vlm.CompleteVision(ctx, d.vlmModel, systemPrompt, prompt, []string{b64}, 0.3, 1024)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(desc), nil
}

func shouldProcessImage(data []byte) bool {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false
	}
	if cfg.Width < 80 || cfg.Height < 80 {
		return false
	}
	if cfg.Width > cfg.Height*20 || cfg.Height > cfg.Width*20 {
		return false
	}
	return true
}

func chunkMarkdown(text string) []string {
	lines := strings.Split(text, "\n")
	var chunks []string
	var buf strings.Builder
	wordCount := 0
	maxWords := 500
	overlapWords := 100

	flush := func() {
		if buf.Len() > 0 {
			chunks = append(chunks, buf.String())
			buf.Reset()
			wordCount = 0
		}
	}

	for _, line := range lines {
		words := len(strings.Fields(line))
		if wordCount+words > maxWords && wordCount > 0 {
			overlap := storeLastWords(buf.String(), overlapWords)
			flush()
			if overlap != "" {
				buf.WriteString(overlap)
				buf.WriteString("\n\n")
				wordCount = len(strings.Fields(overlap))
			}
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		wordCount += words
	}
	flush()
	if len(chunks) == 0 {
		chunks = []string{text}
	}
	return chunks
}

func storeLastWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) <= n {
		return ""
	}
	return strings.Join(fields[len(fields)-n:], " ")
}

func detectContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	default:
		return "application/octet-stream"
	}
}
