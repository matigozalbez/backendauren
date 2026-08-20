package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/messaging"
)

type CrearNotificacionRequest struct {
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
	Tipo    string `json:"tipo"`              // "general", "usuario", "plan"
	UserID  string `json:"user_id,omitempty"` // requerido si tipo == "usuario"
	Plan    string `json:"plan,omitempty"`    // requerido si tipo == "plan"
}

func CrearNotificacion(fsClient *firestore.Client, msgClient *messaging.Client) http.HandlerFunc {
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
		expiresAt := time.Now().AddDate(0, 0, 30) // Expira en 30 días para el TTL de Firestore

		// 1. Guardar la notificación en Firestore (según corresponda)
		notifDoc := map[string]interface{}{
			"titulo":    req.Titulo,
			"mensaje":   req.Mensaje,
			"fecha":     time.Now(),
			"expiresAt": expiresAt,
			"tipo":      req.Tipo,
			"activa":    true,
		}

		if req.Tipo == "usuario" {
			notifDoc["user_id"] = req.UserID
		} else if req.Tipo == "plan" {
			notifDoc["plan"] = req.Plan
		}

		_, _, err := fsClient.Collection("notificaciones").Add(ctx, notifDoc)
		if err != nil {
			http.Error(w, "error creando notificación en db", http.StatusInternalServerError)
			return
		}

		// 2. Obtener los tokens según el tipo de envío
		var tokens []string

		switch req.Tipo {
		case "general":
			tokensSnap, err := fsClient.Collection("push_tokens").Documents(ctx).GetAll()
			if err != nil {
				log.Printf("error consultando tokens generales: %v", err)
			} else {
				for _, doc := range tokensSnap {
					data := doc.Data()
					if t, ok := data["token"].(string); ok && t != "" {
						tokens = append(tokens, t)
					}
				}
			}

		case "usuario":
			if req.UserID == "" {
				http.Error(w, "user_id es requerido para notificaciones de usuario", http.StatusBadRequest)
				return
			}
			fmt.Println("--- BUSCANDO USUARIO --- ID:", req.UserID)

			docSnap, err := fsClient.Collection("push_tokens").Doc(req.UserID).Get(ctx)
			if err != nil {
				fmt.Println("Error buscando doc en Firestore:", err)
			}

			if err == nil && docSnap.Exists() {
				data := docSnap.Data()
				fmt.Println("¡Doc encontrado! Datos:", data)

				if t, ok := data["token"].(string); ok && t != "" {
					fmt.Println("Token extraído OK:", t)
					tokens = append(tokens, t)
				} else {
					fmt.Println("El campo 'token' no existe o no es string. Data:", data)
				}
			} else {
				fmt.Println("El documento NO existe para el ID:", req.UserID)
			}

		case "plan":
			if req.Plan == "" {
				http.Error(w, "plan es requerido para notificaciones por plan", http.StatusBadRequest)
				return
			}
			fmt.Println("--- BUSCANDO POR PLAN --- Plan solicitado:", req.Plan)

			tokensSnap, err := fsClient.Collection("push_tokens").Where("planes", "array-contains", req.Plan).Documents(ctx).GetAll()
			if err != nil {
				fmt.Println("Error consultando tokens por plan en Firestore:", err)
			} else {
				fmt.Printf("Documentos encontrados con el plan '%s': %d\n", req.Plan, len(tokensSnap))
				for _, doc := range tokensSnap {
					data := doc.Data()
					fmt.Println("Revisando doc ID:", doc.Ref.ID, "-> Data:", data)

					if t, ok := data["token"].(string); ok && t != "" {
						fmt.Println("Token extraído OK para plan:", t)
						tokens = append(tokens, t)
					} else {
						fmt.Println("El campo 'token' no existe o no es string en este doc de plan. Data:", data)
					}
				}
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"push_enviados": pushEnviados,
		})
	}
}
