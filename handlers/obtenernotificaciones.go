package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/iterator"
)

type NotificacionResponse struct {
	ID      string `json:"id"`
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
	Fecha   any    `json:"fecha,omitempty"`
}

func ObtenerNotificaciones(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
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

		var notificaciones []NotificacionResponse

		// Notificaciones generales (para todos)
		qGenerales := fsClient.Collection("notificaciones").
			Where("tipo", "==", "general").
			OrderBy("fecha", firestore.Desc).
			Limit(30)

		iter := qGenerales.Documents(ctx)
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				http.Error(w, "error leyendo notificaciones", http.StatusInternalServerError)
				return
			}
			data := doc.Data()
			notificaciones = append(notificaciones, NotificacionResponse{
				ID:      doc.Ref.ID,
				Titulo:  getString(data["titulo"]),
				Mensaje: getString(data["mensaje"]),
				Fecha:   data["fecha"],
			})
		}

		// Notificaciones dirigidas a este usuario puntual
		qUsuario := fsClient.Collection("notificaciones").
			Where("tipo", "==", "usuario").
			Where("user_id", "==", uid).
			OrderBy("fecha", firestore.Desc).
			Limit(30)

		iterUsuario := qUsuario.Documents(ctx)
		for {
			doc, err := iterUsuario.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				http.Error(w, "error leyendo notificaciones", http.StatusInternalServerError)
				return
			}
			data := doc.Data()
			notificaciones = append(notificaciones, NotificacionResponse{
				ID:      doc.Ref.ID,
				Titulo:  getString(data["titulo"]),
				Mensaje: getString(data["mensaje"]),
				Fecha:   data["fecha"],
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(notificaciones)
	}
}
