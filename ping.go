package main

import (
	"encoding/json"
	"net/http"
)

func pingHandler(w http.ResponseWriter, r *http.Request) {

	setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}
