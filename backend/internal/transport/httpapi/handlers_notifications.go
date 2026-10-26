package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/notify"
	"github.com/locklens/locklens/internal/repository"
)

func (s *Server) notifyError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *notify.ValidationError
	switch {
	case errors.As(err, &ve):
		writeFieldErrors(w, r, ve.Fields)
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "not found")
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	cs, err := s.d.Notify.Channels(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": cs})
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	var id int64
	if r.PathValue("id") != "" {
		var ok bool
		if id, ok = pathID(r, "id"); !ok {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
			return
		}
	}
	var in notify.ChannelInput
	if err := decode(w, r, &in, 16<<10); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	c, err := s.d.Notify.SaveChannel(r.Context(), id, in)
	if err != nil {
		s.notifyError(w, r, err)
		return
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, c)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
		return
	}
	if err := s.d.Notify.DeleteChannel(r.Context(), id); err != nil {
		s.notifyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
		return
	}
	d, err := s.d.Notify.Test(r.Context(), id)
	if err != nil {
		s.notifyError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// listDeliveries returns recent notification deliveries. Everyone who can
// view incidents may see an incident's deliveries; the global list is for
// administrators.
func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	var incident *int64
	if v := r.URL.Query().Get("incident"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "incident must be an integer")
			return
		}
		incident = &id
	} else if !sessionOf(r).Can(auth.PermManageSettings) {
		writeError(w, r, http.StatusForbidden, "forbidden", "your role does not allow this action")
		return
	}
	ds, err := s.d.Notify.Deliveries(r.Context(), incident, 100)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": ds})
}

func (s *Server) listSilences(w http.ResponseWriter, r *http.Request) {
	xs, err := s.d.Notify.Silences(r.Context(), r.URL.Query().Get("all") == "true")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"silences": xs})
}

func (s *Server) createSilence(w http.ResponseWriter, r *http.Request) {
	var in notify.SilenceInput
	if err := decode(w, r, &in, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	x, err := s.d.Notify.CreateSilence(r.Context(), in, sessionOf(r).Username)
	if err != nil {
		s.notifyError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

func (s *Server) endSilence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid silence id")
		return
	}
	if err := s.d.Notify.EndSilence(r.Context(), id); err != nil {
		s.notifyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
