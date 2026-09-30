package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

// rolAdmin es la clave de rol que se guarda en admins.rol. Hoy solo se da
// admin; cuando aparezca el de operador, pasa a ser una decisión de esta
// constante y no de cada handler.
const rolAdmin = "admin"

type OtorgarAdminInput struct {
	Email string `json:"email"`
}

func OtorgarAdmin(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input OtorgarAdminInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Email = strings.TrimSpace(strings.ToLower(input.Email))
		if input.Email == "" {
			http.Error(w, "falta el email", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		user, err := authClient.GetUserByEmail(ctx, input.Email)
		if err != nil {
			http.Error(w, "no se encontró un usuario con ese email", http.StatusNotFound)
			return
		}

		err = authClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
			"admin": true,
		})
		if err != nil {
			http.Error(w, "error asignando admin", http.StatusInternalServerError)
			return
		}

		// El usuario ya existía, así que acá no hay nombre ni apellido en el
		// request: salen del DisplayName de Firebase o, si no tiene, de la
		// tabla socios. admins.nombre y admins.apellido son NOT NULL, así que
		// nombreDelAdmin siempre devuelve algo.
		nombre, apellido := nombreDelAdmin(ctx, input.Email, user.DisplayName)

		if _, err := PGPool.Exec(ctx, `
			INSERT INTO admins (uid, nombre, apellido, email, rol)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (uid) DO UPDATE SET
				nombre = EXCLUDED.nombre,
				apellido = EXCLUDED.apellido,
				email = EXCLUDED.email,
				rol = EXCLUDED.rol`,
			user.UID, nombre, apellido, input.Email, rolAdmin,
		); err != nil {
			// El permiso ya quedó puesto en Firebase, que es lo que deja
			// entrar al panel. Si el registro en admins falla, avisamos y
			// cortamos: seguir y mandar el mail de invitación a alguien que
			// quedó sin registro en la base deja un acceso sin rastro.
			log.Printf("ERROR guardando admin %s: %v", user.UID, err)
			http.Error(w, "permiso asignado, pero falló al guardar el registro en admins", http.StatusInternalServerError)
			return
		}

		// El mail va último y no frena el flujo: el acceso ya está dado, y
		// tirar el request abajo por un problema de Resend sería peor que un
		// mail perdido.
		if err := enviarMailInvitacionAdmin(input.Email, nombre, rolAdmin, "Ingresar al panel", APP_LINK_ADMIN); err != nil {
			log.Printf("OTORGADO sin mail a %s: %v", input.Email, err)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"email":  input.Email,
			"uid":    user.UID,
		})
	}
}

// De yapa, para poder sacarle el admin a alguien sin tocar consola:
func RevocarAdmin(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input OtorgarAdminInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Email = strings.TrimSpace(strings.ToLower(input.Email))
		if input.Email == "" {
			http.Error(w, "falta el email", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		user, err := authClient.GetUserByEmail(ctx, input.Email)
		if err != nil {
			http.Error(w, "no se encontró un usuario con ese email", http.StatusNotFound)
			return
		}

		err = authClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
			"admin": false,
		})
		if err != nil {
			http.Error(w, "error revocando admin", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}