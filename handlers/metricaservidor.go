package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/matigozalbez/backendauren/utils"
)

func MetricasServidor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return	
	}

	metrics, err := utils.GetServerMetrics()
	if err != nil {
		http.Error(w, "error obteniendo métricas: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(metrics)
}