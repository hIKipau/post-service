package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxBodyBytes = 64 << 10
	maxTextRunes = 10000
)

type textRequest struct {
	Text string `json:"text"`
}

// pathID parses a nonzero post UUID from the URL, writing a 400 response and returning false on invalid input.
func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("postID"))
	if err != nil || id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid post ID")
		return uuid.Nil, false
	}
	return id, true
}

// queryInt reads a single integer query parameter within inclusive bounds, using fallback when the key is absent.
// Invalid or repeated values produce a 400 response and a false result.
func queryInt(w http.ResponseWriter, r *http.Request, key string, fallback, min, max int64) (int64, bool) {
	values, exists := r.URL.Query()[key]
	if !exists {
		return fallback, true
	}
	if len(values) == 1 {
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil && value >= min && value <= max {
			return value, true
		}
	}
	writeError(w, http.StatusBadRequest, "invalid "+key)
	return 0, false
}

// readText decodes a JSON body containing only text and validates its content type, size and text length.
// On invalid input it writes an error response and returns false.
func readText(w http.ResponseWriter, r *http.Request) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return "", false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body textRequest
	if err := decoder.Decode(&body); err != nil {
		bodyError(w, err)
		return "", false
	}
	// Reject trailing JSON values and enforce the size limit on the whole body.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		bodyError(w, err)
		return "", false
	}
	if strings.TrimSpace(body.Text) == "" || utf8.RuneCountInString(body.Text) > maxTextRunes {
		writeError(w, http.StatusBadRequest, "text must contain 1 to 10000 characters and not be blank")
		return "", false
	}
	return body.Text, true
}

// bodyError returns 413 for a body size limit error and 400 for other JSON decoding errors.
func bodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body; expected only text")
}
