package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
)

type CatalogoPlanInput struct {
	Nombre     string         `json:"nombre"`
	Beneficios map[string]int `json:"beneficios"`
}

func CrearOActualizarCatalogoPlan(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input CatalogoPlanInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Nombre == "" {
			http.Error(w, "falta el nombre del plan", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		_, err := fsClient.Collection("catalogo_planes").Doc(input.Nombre).Set(ctx, map[string]interface{}{
			"nombre":     input.Nombre,
			"beneficios": input.Beneficios,
		})
		if err != nil {
			http.Error(w, "error guardando catálogo de plan", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ObtenerCatalogoPlan(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nombre := r.URL.Query().Get("nombre")
		if nombre == "" {
			http.Error(w, "falta el nombre del plan", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		docSnap, err := fsClient.Collection("catalogo_planes").Doc(nombre).Get(ctx)
		if err != nil || !docSnap.Exists() {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"beneficios": map[string]int{}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(docSnap.Data())
	}
}
