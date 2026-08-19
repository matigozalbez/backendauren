package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/messaging"
)

type CrearNotificacionRequest struct {
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
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
		if req.Titulo == "" || req.Mensaje == "" {
			http.Error(w, "titulo y mensaje son requeridos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// 1. Guardar la notificación en Firestore
		_, _, err := fsClient.Collection("notificaciones").Add(ctx, map[string]interface{}{
			"titulo":  req.Titulo,
			"mensaje": req.Mensaje,
			"fecha":   time.Now(),
			"activa":  true,
		})
		if err != nil {
			http.Error(w, "error creando notificación", http.StatusInternalServerError)
			return
		}

		// 2. Obtener los tokens de la colección push_tokens
		tokensSnap, err := fsClient.Collection("push_tokens").Documents(ctx).GetAll()
		if err != nil {
			log.Printf("error obteniendo tokens: %v", err)
		}

		var tokens []string
		for _, doc := range tokensSnap {
			data := doc.Data()
			if t, ok := data["token"].(string); ok && t != "" {
				tokens = append(tokens, t)
			}
		}

		pushEnviados := 0
		if len(tokens) > 0 && msgClient != nil {
			// Enviamos uno por uno para asegurar que el formato Webpush llegue limpio a las PWA
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
			log.Printf("push enviados con éxito: %d de %d", pushEnviados, len(tokens))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"push_enviados": pushEnviados,
		})
	}
}
