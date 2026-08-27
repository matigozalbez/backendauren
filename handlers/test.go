package handlers

import (
	"net/http"
)

func HandleTestStress(w http.ResponseWriter, r *http.Request) {
	counter := 0
	for i := 0; i < 500000; i++ {
		counter += i
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"VPS viva"}`))
}