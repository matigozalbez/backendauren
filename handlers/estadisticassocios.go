package handlers

import (
	"encoding/json"
	"net/http"
)

func EstadisticasSocios(w http.ResponseWriter, r *http.Request) {
	stats, err := leerStats()
	if err != nil {
		http.Error(w, "error leyendo estadísticas", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
