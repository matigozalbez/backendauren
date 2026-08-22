package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
)

type ActualizarBeneficiosInput struct {
	Plan       string   `json:"plan"`
	Beneficios []string `json:"beneficios"`
}

func ActualizarBeneficiosSocio(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		dni := r.URL.Query().Get("dni")
		if dni == "" {
			http.Error(w, "falta el dni", http.StatusBadRequest)
			return
		}

		var input ActualizarBeneficiosInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		if input.Plan == "" {
			http.Error(w, "falta el plan", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		_, err := fsClient.Collection("socios").Doc(dni).Update(ctx, []firestore.Update{
			{Path: "beneficios." + input.Plan, Value: input.Beneficios},
		})
		if err != nil {
			http.Error(w, "error actualizando beneficios", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
