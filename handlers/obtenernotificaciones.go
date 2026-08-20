package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

type NotificacionResponse struct {
	ID      string `json:"id"`
	Titulo  string `json:"titulo"`
	Mensaje string `json:"mensaje"`
	Fecha   any    `json:"fecha,omitempty"`
}

func ObtenerNotificaciones(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()

		q := fsClient.Collection("notificaciones").OrderBy("fecha", firestore.Desc).Limit(30)
		iter := q.Documents(ctx)

		var notificaciones []NotificacionResponse

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

			notif := NotificacionResponse{
				ID:      doc.Ref.ID,
				Titulo:  getString(data["titulo"]),
				Mensaje: getString(data["mensaje"]),
				Fecha:   data["fecha"],
			}
			notificaciones = append(notificaciones, notif)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(notificaciones)
	}
}
