package handlers

import (
	"context"
	"encoding/json"
	"net/http"
)

type ActualizarBeneficiosInput struct {
	Plan       string   `json:"plan"`
	Beneficios []string `json:"beneficios"`
}

func ActualizarBeneficiosSocio() http.HandlerFunc {
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

		beneficiosJSON, err := json.Marshal(input.Beneficios)
		if err != nil {
			http.Error(w, "beneficios inválidos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		_, err = PGPool.Exec(ctx,
			`UPDATE socios SET
				beneficios = jsonb_set(COALESCE(beneficios, '{}'::jsonb), ARRAY[$1], to_jsonb($2::jsonb), TRUE),
				actualizado_en = now()
			 WHERE dni = $3`,
			input.Plan, string(beneficiosJSON), dni,
		)
		if err != nil {
			http.Error(w, "error actualizando beneficios", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionSocioBeneficios, "socio", dni, map[string]any{
			"plan":         input.Plan,
			"n_beneficios": len(input.Beneficios),
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}