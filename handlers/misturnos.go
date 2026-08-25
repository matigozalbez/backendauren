package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/iterator"
)

func MisTurnos(
	fsClient *firestore.Client,
	authClient *auth.Client,
) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(
				w,
				"método no permitido",
				http.StatusMethodNotAllowed,
			)
			return
		}

		uid, err := verifyIDToken(r, authClient)

		if err != nil {
			http.Error(
				w,
				"no autorizado, iniciá sesión",
				http.StatusUnauthorized,
			)
			return
		}

		ctx := context.Background()

		query := fsClient.
			Collection("turnos").
			Where("uid", "==", uid).
			OrderBy("creadoEn", firestore.Desc)

		iter := query.Documents(ctx)
		defer iter.Stop()

		turnos := make([]TurnoAdminView, 0)

		for {

			doc, err := iter.Next()

			if err == iterator.Done {
				break
			}

			if err != nil {
				http.Error(
					w,
					"error leyendo tus turnos",
					http.StatusInternalServerError,
				)
				return
			}

			var turno TurnoAdminView

			data := doc.Data()

			b, err := json.Marshal(data)

			if err != nil {
				continue
			}

			if err := json.Unmarshal(b, &turno); err != nil {
				continue
			}

			turno.ID = doc.Ref.ID

			turnos = append(turnos, turno)
		}

		json.NewEncoder(w).Encode(turnos)
	}
}