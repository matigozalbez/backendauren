package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
)

type TurnoInput struct {
	Especialidad string `json:"especialidad"`
	Ciudad       string `json:"ciudad"`
	Direccion    string `json:"direccion"`
	Motivo       string `json:"motivo"`

	// Si es para un adherente, mandar su DNI. Si va vacío, el turno es para el titular.
	AdherenteDni string `json:"adherenteDni"`
}

type Adherente struct {
	Dni         string `firestore:"dni"`
	Nombre      string `firestore:"nombre"`
	Apellido    string `firestore:"apellido"`
	Parentesco  string `firestore:"parentesco"`
}

func CrearTurno(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		var input TurnoInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Especialidad = strings.TrimSpace(input.Especialidad)
		input.Ciudad = strings.TrimSpace(input.Ciudad)
		input.Direccion = strings.TrimSpace(input.Direccion)
		input.AdherenteDni = strings.TrimSpace(input.AdherenteDni)

		if input.Especialidad == "" || input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// Buscamos al socio (titular) por su uid de Firebase, NO confiamos en un DNI que mande el front.
		iter := fsClient.Collection("socios").Where("uid", "==", uid).Limit(1).Documents(ctx)
		snap, err := iter.Next()
		if err != nil {
			http.Error(w, "no se encontró el socio asociado a esta cuenta", http.StatusNotFound)
			return
		}

		socioData := snap.Data()
		socioDni, _ := socioData["dni"].(string)
		socioNombre, _ := socioData["nombre"].(string)
		socioApellido, _ := socioData["apellido"].(string)
		nombreCompleto := strings.TrimSpace(socioNombre + " " + socioApellido)

		// Datos que van al documento del turno. Por defecto es para el titular.
		beneficiarioDni := socioDni
		beneficiarioNombre := nombreCompleto
		esParaAdherente := false

		if input.AdherenteDni != "" {
			var adherentes []Adherente
			if raw, ok := socioData["adherentes"]; ok {
				// raw viene como []interface{} desde Firestore, lo reconvertimos.
				b, _ := json.Marshal(raw)
				_ = json.Unmarshal(b, &adherentes)
			}

			var encontrado *Adherente
			for i := range adherentes {
				if adherentes[i].Dni == input.AdherenteDni {
					encontrado = &adherentes[i]
					break
				}
			}

			if encontrado == nil {
				http.Error(w, "el adherente indicado no pertenece a tu grupo familiar", http.StatusForbidden)
				return
			}

			esParaAdherente = true
			beneficiarioDni = encontrado.Dni
			beneficiarioNombre = strings.TrimSpace(encontrado.Nombre + " " + encontrado.Apellido)
		}

		doc := fsClient.Collection("turnos").NewDoc()

		_, err = doc.Set(ctx, map[string]interface{}{
			"uid":                 uid,
			"socioDni":            socioDni,
			"solicitadoPor":       nombreCompleto,
			"esParaAdherente":     esParaAdherente,
			"beneficiarioDni":     beneficiarioDni,
			"beneficiarioNombre":  beneficiarioNombre,
			"especialidad":        input.Especialidad,
			"ciudad":              input.Ciudad,
			"direccion":           input.Direccion,
			"motivo":              input.Motivo,
			"estado":              "pendiente",
			"creadoEn":            firestore.ServerTimestamp,
		})
		if err != nil {
			http.Error(w, "error al pedir el turno", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"id":     doc.ID,
		})
	}
}



