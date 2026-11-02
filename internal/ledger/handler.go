package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxBody bounds request bodies.
const maxBody = 1 << 20

// Handler serves the ledger's HTTP JSON API. It decodes strictly (unknown
// fields are rejected) and maps errors to {"error":{"code","message"}}.
type Handler struct {
	svc *Service
}

// NewHandler wraps svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes adds the ledger routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/jobs", h.create)
	mux.HandleFunc("GET /v1/jobs", h.list)
	mux.HandleFunc("GET /v1/jobs/{id}", h.get)
	mux.HandleFunc("PATCH /v1/jobs/{id}", h.edit)
	mux.HandleFunc("GET /v1/jobs/{id}/events", h.events)
	mux.HandleFunc("POST /v1/jobs/{id}/claim", h.claim)
	mux.HandleFunc("POST /v1/jobs/{id}/renew", h.renew)
	mux.HandleFunc("POST /v1/jobs/{id}/release", h.release)
	mux.HandleFunc("POST /v1/jobs/{id}/finish", h.finish)
	mux.HandleFunc("POST /v1/jobs/{id}/abandon", h.abandon)
}

// ErrorBody is the error half of every failed response.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, StatusOf(err), errorEnvelope{Error: ErrorBody{Code: CodeOf(err), Message: err.Error()}})
}

// DecodeStrict decodes exactly one JSON value into target, rejecting unknown
// fields. Errors are validation_failed.
func DecodeStrict(r io.Reader, target any) error {
	dec := json.NewDecoder(io.LimitReader(r, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return validationFailed(fmt.Errorf("decode request: %w", err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return validationFailed(errors.New("request body must hold a single JSON value"))
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, validationFailed(fmt.Errorf("job id %q is not a positive integer", r.PathValue("id")))
	}
	return id, nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := DecodeStrict(r.Body, &req); err != nil {
		writeError(w, err)
		return
	}
	j, err := h.svc.Create(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

var listParams = map[string]bool{"status": true, "kind": true, "assignee": true, "requested_by": true, "parent_id": true, "max_priority": true, "limit": true, "offset": true}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if !listParams[k] {
			writeError(w, validationFailed(fmt.Errorf("unknown query parameter %q", k)))
			return
		}
	}
	req := ListRequest{Assignee: q.Get("assignee"), RequestedBy: q.Get("requested_by")}
	for _, p := range []struct {
		name string
		dst  *[]string
	}{{"status", &req.Statuses}, {"kind", &req.Kinds}} {
		for _, v := range q[p.name] {
			for _, part := range strings.Split(v, ",") {
				if part = strings.TrimSpace(part); part != "" {
					*p.dst = append(*p.dst, part)
				}
			}
		}
	}
	ints := []struct {
		name string
		dst  *int
	}{{"max_priority", &req.MaxPriority}, {"limit", &req.Limit}, {"offset", &req.Offset}}
	for _, p := range ints {
		if v := q.Get(p.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				writeError(w, validationFailed(fmt.Errorf("%s %q is not an integer", p.name, v)))
				return
			}
			*p.dst = n
		}
	}
	if v := q.Get("parent_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, validationFailed(fmt.Errorf("parent_id %q is not an integer", v)))
			return
		}
		req.ParentID = &n
	}
	page, err := h.svc.List(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	j, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	evs, err := h.svc.Events(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// mutate decodes a request for job {id}, calls fn and writes its result.
func mutate[Req any, Res any](w http.ResponseWriter, r *http.Request, fn func(id int64, req Req) (Res, error)) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req Req
	if err := DecodeStrict(r.Body, &req); err != nil {
		writeError(w, err)
		return
	}
	res, err := fn(id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req EditRequest) (Job, error) { return h.svc.Edit(r.Context(), id, req) })
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req ClaimRequest) (Claim, error) { return h.svc.Claim(r.Context(), id, req) })
}

func (h *Handler) renew(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req RenewRequest) (Job, error) { return h.svc.Renew(r.Context(), id, req) })
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req ReleaseRequest) (Job, error) { return h.svc.Release(r.Context(), id, req) })
}

func (h *Handler) finish(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req FinishRequest) (Job, error) { return h.svc.Finish(r.Context(), id, req) })
}

func (h *Handler) abandon(w http.ResponseWriter, r *http.Request) {
	mutate(w, r, func(id int64, req AbandonRequest) (Job, error) { return h.svc.Abandon(r.Context(), id, req) })
}
