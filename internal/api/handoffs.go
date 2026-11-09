package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/johnreginald/donewhen/internal/auth"
	"github.com/johnreginald/donewhen/internal/service"
	"github.com/johnreginald/donewhen/internal/store"
)

// The handoff of a project (initiative): GET the latest or one version, list
// the versions, PUT a new version. Each is scoped to the workspace, so another
// workspace's initiative answers 404.

func (s *Server) handleGetHandoff(w http.ResponseWriter, r *http.Request) {
	version := 0
	if v := r.URL.Query().Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "version must be a positive number")
			return
		}
		version = n
	}
	h, err := s.store.GetHandoff(r.Context(), ws(r), r.PathValue("id"), version)
	if handleStoreErr(w, err) {
		return
	}
	// done_tickets is live: the tickets In flight that are Done or Canceled now.
	writeJSON(w, http.StatusOK, service.HandoffSaved{Handoff: h, DoneTickets: s.svc.HandoffDoneTickets(r.Context(), ws(r), h.BodyMD)})
}

func (s *Server) handleListHandoffVersions(w http.ResponseWriter, r *http.Request) {
	hs, err := s.store.ListHandoffVersions(r.Context(), ws(r), r.PathValue("id"))
	if handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, hs)
}

func (s *Server) handlePutHandoff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Body    string `json:"body"`
		Summary string `json:"summary"`
		// Legacy skips the structure checks. Only the import CLI sets it.
		Legacy bool `json:"legacy"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	h, err := s.svc.SaveHandoff(r.Context(), ws(r), r.PathValue("id"), req.Body, req.Summary, auth.ActorFrom(r.Context()), req.Legacy)
	var fe *service.HandoffFormatError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fe.Error(), "code": fe.Code, "missing": fe.Missing, "lines": fe.Lines, "chars": fe.Chars,
		})
		return
	}
	if errors.Is(err, store.ErrInvalidHandoff) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "invalid_handoff"})
		return
	}
	if handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleHandoffFormat hands out the template, rules and limits, so the page
// never keeps its own copy.
func (s *Server) handleHandoffFormat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, service.HandoffFormatInfo())
}
