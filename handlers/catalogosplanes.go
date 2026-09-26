package handlers

import (
	"context"
	"encoding/json"
	"net/http"
)

type BeneficioInput struct {
	Clave         string   `json:"clave"`
	Titulo        string   `json:"titulo"`
	Descripciones []string `json:"descripciones"`
}

type CatalogoPlanInput struct {
	Nombre     string           `json:"nombre"`
	Beneficios []BeneficioInput `json:"beneficios"`
}

func CrearOActualizarCatalogoPlan() http.HandlerFunc {
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

		beneficiosData := make([]map[string]interface{}, 0, len(input.Beneficios))
		for _, b := range input.Beneficios {
			beneficiosData = append(beneficiosData, map[string]interface{}{
				"clave":         b.Clave,
				"titulo":        b.Titulo,
				"descripciones": b.Descripciones,
			})
		}
		beneficiosJSON, err := json.Marshal(beneficiosData)
		if err != nil {
			http.Error(w, "beneficios inválidos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		_, err = PGPool.Exec(ctx, `
			INSERT INTO catalogos_planes (nombre, beneficios)
			VALUES ($1, $2)
			ON CONFLICT (nombre) DO UPDATE SET
				beneficios = EXCLUDED.beneficios,
				actualizado_en = now()`,
			input.Nombre, beneficiosJSON,
		)
		if err != nil {
			http.Error(w, "error guardando catálogo de plan", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ObtenerCatalogoPlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nombre := r.URL.Query().Get("nombre")
		if nombre == "" {
			http.Error(w, "falta el nombre del plan", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		var beneficios []byte
		err := PGPool.QueryRow(ctx,
			`SELECT beneficios FROM catalogos_planes WHERE nombre = $1`, nombre,
		).Scan(&beneficios)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"beneficios": []interface{}{}})
			return
		}

		var beneficiosLista []interface{}
		_ = json.Unmarshal(beneficios, &beneficiosLista)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"nombre":     nombre,
			"beneficios": beneficiosLista,
		})
	}
}

func ListarCatalogoPlanes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		rows, err := PGPool.Query(ctx,
			`SELECT nombre, beneficios FROM catalogos_planes ORDER BY nombre`)
		if err != nil {
			http.Error(w, "error obteniendo catálogo", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		planes := []map[string]interface{}{}
		for rows.Next() {
			var nombre string
			var beneficios []byte
			if err := rows.Scan(&nombre, &beneficios); err != nil {
				continue
			}
			var benefLista []interface{}
			_ = json.Unmarshal(beneficios, &benefLista)
			planes = append(planes, map[string]interface{}{
				"nombre":     nombre,
				"beneficios": benefLista,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(planes)
	}
}