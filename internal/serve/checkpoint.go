package serve

import "net/http"

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "checkpoint saving is not implemented", http.StatusNotImplemented)
}
