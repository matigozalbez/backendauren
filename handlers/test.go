package main

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// Tu handler de prueba de estrés
func handleTestStress(w http.ResponseWriter, r *http.Request) {
    counter := 0
    for i := 0; i < 500000; i++ {
        counter += i
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(`{"status":"ok"}`))
}

// Y para registrarlo en tu ServeMux:
mux.HandleFunc("GET /api/test-stress", handleTestStress)