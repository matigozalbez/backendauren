package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"firebase.google.com/go/v4/messaging"
)

type CrearNotificacionRequest struct {
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
	Tipo    string `json:"tipo"`              // "general", "usuario", "plan"
	UserID  string `json:"user_id,omitempty"` // requerido si tipo == "usuario"
	Plan    string `json:"plan,omitempty"`    // requerido si tipo == "plan"
}

func CrearNotificacion(msgClient *messaging.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var req CrearNotificacionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "body inválido", http.StatusBadRequest)
			return
		}

		if req.Titulo == "" || req.Mensaje == "" || req.Tipo == "" {
			http.Error(w, "titulo, mensaje y tipo son requeridos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		expiresAt := time.Now().AddDate(0, 0, 30)

		// 1. Guardar la notificación en PostgreSQL (según corresponda)
		query := `INSERT INTO notificaciones (titulo, mensaje, fecha, expires_at, tipo, activa, user_id, plan)
				  VALUES ($1, $2, now(), $3, $4, TRUE, $5, $6)`

		var userID, plan *string
		if req.Tipo == "usuario" {
			userID = &req.UserID
		} else if req.Tipo == "plan" {
			plan = &req.Plan
		}

		if _, err := PGPool.Exec(ctx, query, req.Titulo, req.Mensaje, expiresAt, req.Tipo, userID, plan); err != nil {
			log.Printf("error creando notificación en PG: %v", err)
			http.Error(w, "error creando notificación en db", http.StatusInternalServerError)
			return
		}

		// 2. Obtener los tokens según el tipo de envío
		var tokens []string

		switch req.Tipo {
		case "general":
			rows, err := PGPool.Query(ctx, `SELECT token FROM push_tokens WHERE token <> ''`)
			if err != nil {
				log.Printf("error consultando tokens generales: %v", err)
			} else {
				for rows.Next() {
					var t string
					if err := rows.Scan(&t); err == nil && t != "" {
						tokens = append(tokens, t)
					}
				}
				rows.Close()
			}

		case "usuario":
			if req.UserID == "" {
				http.Error(w, "user_id es requerido para notificaciones de usuario", http.StatusBadRequest)
				return
			}

			var token string
			err := PGPool.QueryRow(ctx,
				`SELECT token FROM push_tokens WHERE uid = $1`, req.UserID,
			).Scan(&token)
			if err != nil {
				log.Printf("ERROR: no se encontró push token para uid=%s: %v", req.UserID, err)
			} else if token != "" {
				tokens = append(tokens, token)
			}

		case "plan":
			if req.Plan == "" {
				http.Error(w, "plan es requerido para notificaciones por plan", http.StatusBadRequest)
				return
			}
			planJSON, _ := json.Marshal([]string{req.Plan})
			rows, err := PGPool.Query(ctx,
				`SELECT token FROM push_tokens WHERE planes @> $1::jsonb`, string(planJSON),
			)
			if err != nil {
				log.Printf("error consultando tokens por plan: %v", err)
			} else {
				for rows.Next() {
					var t string
					if err := rows.Scan(&t); err == nil && t != "" {
						tokens = append(tokens, t)
					}
				}
				rows.Close()
			}

		default:
			http.Error(w, "tipo de notificación inválido", http.StatusBadRequest)
			return
		}

		// 3. Disparar los Web Push a los tokens filtrados
		pushEnviados := 0
		if len(tokens) > 0 && msgClient != nil {
			for _, token := range tokens {
				message := &messaging.Message{
					Token: token,
					Webpush: &messaging.WebpushConfig{
						Notification: &messaging.WebpushNotification{
							Title: req.Titulo,
							Body:  req.Mensaje,
							Icon:  "/icon-192.png",
						},
					},
				}

				_, err := msgClient.Send(ctx, message)
				if err != nil {
					log.Printf("error enviando push a token: %v", err)
				} else {
					pushEnviados++
				}
			}
			log.Printf("push [%s] enviados con éxito: %d de %d", req.Tipo, pushEnviados, len(tokens))
		}

		// Sin titulo ni mensaje en la auditoría: es contenido editable por el
		// admin y la tabla es permanente y consultable desde el panel.
		registrarAuditoria(r, AccionNotifCrear, "notificacion", "", map[string]any{
			"tipo":          req.Tipo,
			"plan":          req.Plan,
			"user_id":       req.UserID,
			"push_enviados": pushEnviados,
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"push_enviados": pushEnviados,
		})
	}
}