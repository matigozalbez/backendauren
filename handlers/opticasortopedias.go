package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	tipoOptica    = "optica"
	tipoOrtopedia = "ortopedia"
)

type OpticaOrtopediaInput struct {
	Nombre    string `json:"nombre"`
	Tipo      string `json:"tipo"`
	Ciudad    string `json:"ciudad"`
	Provincia string `json:"provincia"`
	Direccion string `json:"direccion"`
	Imagen    string `json:"imagen"`
}

type OpticaOrtopediaView struct {
	ID        int    `json:"id"`
	Nombre    string `json:"nombre"`
	Tipo      string `json:"tipo"`
	Direccion string `json:"direccion"`
	Ciudad    string `json:"ciudad"`
	Provincia string `json:"provincia"`
	Imagen    string `json:"imagen"`
}

func normalizarTipoOpticaOrtopedia(tipo string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tipo)) {
	case tipoOptica:
		return tipoOptica, true
	case tipoOrtopedia:
		return tipoOrtopedia, true
	}
	return "", false
}

func CrearOpticaOrtopedia() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input OpticaOrtopediaInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Nombre = strings.TrimSpace(input.Nombre)
		input.Ciudad = strings.TrimSpace(input.Ciudad)
		input.Provincia = strings.TrimSpace(input.Provincia)
		input.Direccion = strings.TrimSpace(input.Direccion)
		input.Imagen = strings.TrimSpace(input.Imagen)

		tipo, ok := normalizarTipoOpticaOrtopedia(input.Tipo)
		if !ok {
			http.Error(w, "tipo inválido, debe ser 'optica' u 'ortopedia'", http.StatusBadRequest)
			return
		}
		if input.Nombre == "" || input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar %s %s: %v", tipo, input.Nombre, errGeo)
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var id int
		err := PGPool.QueryRow(ctx, `
			INSERT INTO opticas_ortopedias (nombre, tipo, direccion, ciudad, provincia, imagen, lat, lng)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			input.Nombre, tipo, input.Direccion, input.Ciudad, input.Provincia, input.Imagen, lat, lng,
		).Scan(&id)
		if err != nil {
			log.Printf("ERROR insertando óptica/ortopedia %s: %v", input.Nombre, err)
			http.Error(w, "error guardando", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionOpticaOrtopediaCrear, "optica_ortopedia", fmt.Sprintf("%d", id), map[string]any{
			"nombre": input.Nombre,
			"tipo":   tipo,
			"ciudad": input.Ciudad,
		})

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ListarOpticasOrtopedias() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := verifyIDToken(r, AuthClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx, `
			SELECT id, COALESCE(nombre,''), COALESCE(tipo,''), COALESCE(direccion,''),
			       COALESCE(ciudad,''), COALESCE(provincia,''), COALESCE(imagen,'')
			FROM opticas_ortopedias ORDER BY COALESCE(tipo,''), nombre`)
		if err != nil {
			http.Error(w, "error obteniendo ópticas y ortopedias", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		establecimientos := make([]OpticaOrtopediaView, 0)
		for rows.Next() {
			var e OpticaOrtopediaView
			if err := rows.Scan(&e.ID, &e.Nombre, &e.Tipo, &e.Direccion, &e.Ciudad, &e.Provincia, &e.Imagen); err != nil {
				continue
			}
			establecimientos = append(establecimientos, e)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(establecimientos)
	}
}

func EditarOpticaOrtopedia() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID int `json:"id"`
			OpticaOrtopediaInput
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Nombre = strings.TrimSpace(input.Nombre)
		input.Ciudad = strings.TrimSpace(input.Ciudad)
		input.Provincia = strings.TrimSpace(input.Provincia)
		input.Direccion = strings.TrimSpace(input.Direccion)
		input.Imagen = strings.TrimSpace(input.Imagen)

		tipo, ok := normalizarTipoOpticaOrtopedia(input.Tipo)
		if !ok {
			http.Error(w, "tipo inválido, debe ser 'optica' u 'ortopedia'", http.StatusBadRequest)
			return
		}
		if input.ID == 0 || input.Nombre == "" || input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var currentLat, currentLng float64
		err := PGPool.QueryRow(ctx,
			`SELECT lat, lng FROM opticas_ortopedias WHERE id = $1`, input.ID,
		).Scan(&currentLat, &currentLng)
		if err != nil {
			http.Error(w, "óptica/ortopedia no encontrada", http.StatusNotFound)
			return
		}

		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar óptica/ortopedia %d: %v", input.ID, errGeo)
			lat, lng = currentLat, currentLng
		}

		tag, err := PGPool.Exec(ctx, `
			UPDATE opticas_ortopedias SET
				nombre = $1, tipo = $2, direccion = $3, ciudad = $4, provincia = $5,
				imagen = $6, lat = $7, lng = $8
			WHERE id = $9`,
			input.Nombre, tipo, input.Direccion, input.Ciudad, input.Provincia, input.Imagen, lat, lng, input.ID,
		)
		if err != nil {
			log.Printf("ERROR actualizando óptica/ortopedia %d: %v", input.ID, err)
			http.Error(w, "error actualizando", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "óptica/ortopedia no encontrada", http.StatusNotFound)
			return
		}

		registrarAuditoria(r, AccionOpticaOrtopediaEditar, "optica_ortopedia", fmt.Sprintf("%d", input.ID), map[string]any{
			"nombre": input.Nombre,
			"tipo":   tipo,
		})

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func BorrarOpticaOrtopedia() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ID == 0 {
			http.Error(w, "id requerido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx, `DELETE FROM opticas_ortopedias WHERE id = $1`, input.ID)
		if err != nil {
			log.Printf("ERROR borrando óptica/ortopedia %d: %v", input.ID, err)
			http.Error(w, "error al borrar", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "óptica/ortopedia no encontrada", http.StatusNotFound)
			return
		}

		registrarAuditoria(r, AccionOpticaOrtopediaBorrar, "optica_ortopedia", fmt.Sprintf("%d", input.ID), map[string]any{})

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
