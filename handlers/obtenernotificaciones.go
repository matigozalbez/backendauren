package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"firebase.google.com/go/v4/auth"
)

type NotificacionResponse struct {
	ID      string `json:"id"`
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
	Fecha   any    `json:"fecha,omitempty"`
}

func ObtenerNotificaciones(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()

		notificaciones := []NotificacionResponse{}

		leer := func(query string, args ...interface{}) {
			rows, err := PGPool.Query(ctx, query, args...)
			if err != nil {
				log.Printf("ERROR leyendo notificaciones: %v", err)
				return
			}
			defer rows.Close()

			for rows.Next() {
				var n NotificacionResponse
				var fecha time.Time
				if err := rows.Scan(&n.ID, &n.Titulo, &n.Mensaje, &fecha); err != nil {
					continue
				}
				n.Fecha = fecha
				notificaciones = append(notificaciones, n)
			}
		}

		// 1. Generales (últimas 30)
		leer(`
			SELECT id::text, titulo, mensaje, fecha
			FROM notificaciones
			WHERE tipo = 'general'
			ORDER BY fecha DESC
			LIMIT 30`)

		// 2. Propias del usuario (últimas 30)
		leer(`
			SELECT id::text, titulo, mensaje, fecha
			FROM notificaciones
			WHERE tipo = 'usuario' AND user_id = $1
			ORDER BY fecha DESC
			LIMIT 30`, uid)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(notificaciones)
	}
}