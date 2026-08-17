// Package httpapi exposes the INI parse/merge service over HTTP. The service is
// stateless: every request carries its full input in the body.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"task043-inimerge/internal/ini"
)

// API is the HTTP façade for the ini package.
type API struct {
	parseCache map[string]*ini.Document
}

// New creates an API.
func New() *API { return &API{parseCache: map[string]*ini.Document{}} }

// Handler returns the HTTP handler for all routes.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /parse", a.parse)
	mux.HandleFunc("POST /merge", a.merge)
	return mux
}

// writeJSON encodes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a size-limited JSON body into dst with strict field
// checking. It reports whether decoding succeeded.
func decodeJSON(r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst) == nil
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// parseRequest is the body for POST /parse.
type parseRequest struct {
	Content string `json:"content"`
}

func (a *API) parse(w http.ResponseWriter, r *http.Request) {
	var req parseRequest
	if !decodeJSON(r, &req) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON", "status": http.StatusBadRequest})
		return
	}
	doc, ok := a.parseCache[req.Content]
	var err error
	if !ok {
		doc, err = ini.Parse(req.Content)
	}
	if err != nil {
		var pe *ini.ParseError
		if errors.As(err, &pe) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": pe.Msg, "line": pe.Line})
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	a.parseCache[req.Content] = doc
	writeJSON(w, http.StatusOK, map[string]any{"sections": doc.Sections})
}

// configInput is one named INI fragment in a merge request.
type configInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// mergeRequest is the body for POST /merge.
type mergeRequest struct {
	Configs  []configInput `json:"configs"`
	Strategy string        `json:"strategy"`
}

func (a *API) merge(w http.ResponseWriter, r *http.Request) {
	var req mergeRequest
	if !decodeJSON(r, &req) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON", "status": http.StatusBadRequest})
		return
	}
	if len(req.Configs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "configs 不能为空", "status": http.StatusBadRequest})
		return
	}
	strategy, ok := ini.ParseStrategy(req.Strategy)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "strategy 缺失或非法", "status": http.StatusBadRequest})
		return
	}
	for _, c := range req.Configs {
		if c.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "配置 name 不能为空", "status": http.StatusBadRequest})
			return
		}
	}

	docs := make([]ini.NamedDoc, 0, len(req.Configs))
	for _, c := range req.Configs {
		doc, err := ini.Parse(c.Content)
		if err != nil {
			var pe *ini.ParseError
			if errors.As(err, &pe) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": pe.Msg, "config": c.Name, "line": pe.Line})
				return
			}
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "config": c.Name})
			return
		}
		docs = append(docs, ini.NamedDoc{Name: c.Name, Document: doc})
	}

	result, err := ini.Merge(docs, strategy)
	if err != nil {
		var ce *ini.ConflictError
		if errors.As(err, &ce) {
			writeJSON(w, http.StatusConflict, map[string]any{"conflicts": ce.Conflicts})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
