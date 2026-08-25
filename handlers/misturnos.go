package handlers

import (
	"context"
	"encoding/json"
	"log"
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

		log.Printf("MIS TURNOS: request recibida")

		if r.Method != http.MethodGet {

			log.Printf(
				"MIS TURNOS: método no permitido: %s",
				r.Method,
			)

			http.Error(
				w,
				"método no permitido",
				http.StatusMethodNotAllowed,
			)

			return
		}

		uid, err := verifyIDToken(r, authClient)

		if err != nil {

			log.Printf(
				"MIS TURNOS: error verificando token: %v",
				err,
			)

			http.Error(
				w,
				"no autorizado, iniciá sesión",
				http.StatusUnauthorized,
			)

			return
		}

		log.Printf(
			"MIS TURNOS: UID autenticado: %s",
			uid,
		)

		ctx := context.Background()

		log.Printf(
			"MIS TURNOS: consultando Firestore para uid=%s",
			uid,
		)

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

				log.Printf(
					"MIS TURNOS: ERROR FIRESTORE: %v",
					err,
				)

				http.Error(
					w,
					"error leyendo tus turnos: "+err.Error(),
					http.StatusInternalServerError,
				)

				return
			}

			log.Printf(
				"MIS TURNOS: turno encontrado: %s",
				doc.Ref.ID,
			)

			var turno TurnoAdminView

			data := doc.Data()

			b, err := json.Marshal(data)

			if err != nil {

				log.Printf(
					"MIS TURNOS: error marshal turno %s: %v",
					doc.Ref.ID,
					err,
				)

				continue
			}

			if err := json.Unmarshal(b, &turno); err != nil {

				log.Printf(
					"MIS TURNOS: error unmarshal turno %s: %v",
					doc.Ref.ID,
					err,
				)

				continue
			}

			turno.ID = doc.Ref.ID

			turnos = append(turnos, turno)
		}

		log.Printf(
			"MIS TURNOS: encontrados %d turnos para uid=%s",
			len(turnos),
			uid,
		)

		if err := json.NewEncoder(w).Encode(turnos); err != nil {

			log.Printf(
				"MIS TURNOS: error enviando respuesta: %v",
				err,
			)

			return
		}
	}
}