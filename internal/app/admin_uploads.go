package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"grep-offer/internal/store"
)

const maxEditorUploadBytes int64 = 8 << 20

var allowedEditorImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

func (a *App) handleAdminImageUpload(w http.ResponseWriter, r *http.Request) {
	actor := a.requireAdmin(w, r)
	if actor == nil {
		return
	}
	if strings.TrimSpace(a.uploadsDir) == "" {
		writeUploadError(w, http.StatusServiceUnavailable, "uploads dir is not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEditorUploadBytes)
	if err := r.ParseMultipartForm(maxEditorUploadBytes); err != nil {
		writeUploadError(w, http.StatusRequestEntityTooLarge, "Файл слишком большой. Лимит 8 MB.")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeUploadError(w, http.StatusBadRequest, "Нужно выбрать картинку.")
		return
	}
	defer file.Close()

	payload, err := io.ReadAll(file)
	if err != nil {
		writeUploadError(w, http.StatusBadRequest, "Не удалось прочитать файл.")
		return
	}
	if len(payload) == 0 {
		writeUploadError(w, http.StatusBadRequest, "Пустой файл не подойдет.")
		return
	}

	contentType := http.DetectContentType(payload)
	ext, ok := allowedEditorImageTypes[contentType]
	if !ok {
		writeUploadError(w, http.StatusUnsupportedMediaType, "Поддерживаются только PNG, JPG, WEBP и GIF.")
		return
	}

	fileName, err := buildEditorUploadFileName(header.Filename, ext)
	if err != nil {
		writeUploadError(w, http.StatusInternalServerError, "Не удалось подготовить имя файла.")
		return
	}

	targetDir, err := safeUploadDir(filepath.Join(strings.TrimSpace(a.uploadsDir), "editor"))
	if err != nil {
		writeUploadError(w, http.StatusInternalServerError, "Проблема с uploads dir.")
		return
	}
	if err := os.MkdirAll(targetDir, 0o775); err != nil {
		writeUploadError(w, http.StatusInternalServerError, "Не удалось подготовить каталог uploads.")
		return
	}

	targetPath := filepath.Join(targetDir, fileName)
	if err := os.WriteFile(targetPath, payload, 0o664); err != nil {
		writeUploadError(w, http.StatusInternalServerError, "Не удалось сохранить картинку.")
		return
	}

	publicPath := "/uploads/editor/" + fileName
	a.writeAuditLog(r.Context(), r, actor, store.AuditLogInput{
		Scope:      "admin",
		Action:     "image_uploaded",
		TargetType: "upload",
		TargetKey:  publicPath,
		Details: map[string]string{
			"content_type": contentType,
			"size":         strconvItoa(len(payload)),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}{
		Path:     publicPath,
		Markdown: "![](" + publicPath + ")",
	})
}

func buildEditorUploadFileName(originalName, ext string) (string, error) {
	base := sanitizeUploadBaseName(strings.TrimSuffix(filepath.Base(strings.TrimSpace(originalName)), filepath.Ext(strings.TrimSpace(originalName))))
	if base == "" {
		base = "image"
	}

	token, err := generateSessionToken()
	if err != nil {
		return "", err
	}

	if len(token) > 10 {
		token = token[:10]
	}

	return base + "-" + token + ext, nil
}

func sanitizeUploadBaseName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		isAlphaNum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if isAlphaNum {
			builder.WriteRune(r)
			lastDash = false
			continue
		}

		if !lastDash {
			builder.WriteRune('-')
			lastDash = true
		}
	}

	result := strings.Trim(builder.String(), "-")
	if len(result) > 48 {
		result = strings.Trim(result[:48], "-")
	}

	return result
}

func safeUploadDir(dir string) (string, error) {
	baseDir, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return "", err
	}

	parent := filepath.Dir(baseDir)
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	prefix := parentAbs + string(os.PathSeparator)
	if baseDir != parentAbs && !strings.HasPrefix(baseDir, prefix) {
		return "", errors.New("upload dir escapes base")
	}

	return baseDir, nil
}

func writeUploadError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{
		Error: message,
	})
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}

	negative := value < 0
	if negative {
		value = -value
	}

	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}

	return string(digits[pos:])
}
