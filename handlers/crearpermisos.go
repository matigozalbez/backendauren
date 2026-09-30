package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

type CrearAdminInput struct {
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	Email    string `json:"email"`
	Rol      string `json:"rol"` // informativo — el permiso real lo da el custom claim
}

func CrearAdmin(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input CrearAdminInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Nombre = strings.TrimSpace(input.Nombre)
		input.Apellido = strings.TrimSpace(input.Apellido)
		input.Email = strings.TrimSpace(strings.ToLower(input.Email))
		input.Rol = strings.TrimSpace(input.Rol)

		if input.Nombre == "" || input.Apellido == "" || input.Email == "" {
			http.Error(w, "faltan datos (nombre, apellido o email)", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// Si ya existe un usuario con ese email, no lo pisamos — lo tratamos como error explícito.
		if _, err := authClient.GetUserByEmail(ctx, input.Email); err == nil {
			http.Error(w, "ya existe un usuario con ese email", http.StatusConflict)
			return
		}

		tempPassword, err := generarPasswordTemporal()
		if err != nil {
			http.Error(w, "error generando credenciales", http.StatusInternalServerError)
			return
		}

		nombreCompleto := strings.TrimSpace(input.Nombre + " " + input.Apellido)

		newUser := (&auth.UserToCreate{}).
			Email(input.Email).
			Password(tempPassword).
			DisplayName(nombreCompleto).
			EmailVerified(false)

		userRecord, err := authClient.CreateUser(ctx, newUser)
		if err != nil {
			http.Error(w, "error creando el usuario: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := authClient.SetCustomUserClaims(ctx, userRecord.UID, map[string]interface{}{
			"admin": true,
		}); err != nil {
			http.Error(w, "usuario creado, pero falló al asignarle el permiso de admin", http.StatusInternalServerError)
			return
		}

		rol := input.Rol
		if rol == "" {
			rol = rolAdmin
		}

		if _, err := PGPool.Exec(ctx, `
			INSERT INTO admins (uid, nombre, apellido, email, rol)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (uid) DO UPDATE SET
				nombre = EXCLUDED.nombre,
				apellido = EXCLUDED.apellido,
				email = EXCLUDED.email,
				rol = EXCLUDED.rol`,
			userRecord.UID, input.Nombre, input.Apellido, input.Email, rol,
		); err != nil {
			log.Printf("ERROR guardando admin %s: %v", userRecord.UID, err)
			http.Error(w, "usuario creado, pero falló al guardar el registro en admins", http.StatusInternalServerError)
			return
		}

		// Link para que el nuevo admin elija su propia contraseña en vez de usar
		// la temporal. Acá el botón sí es el de reset, a diferencia de
		// OtorgarAdmin, que manda al panel: este usuario todavía no tiene
		// contraseña elegida.
		resetLink, err := authClient.PasswordResetLink(ctx, input.Email)
		if err != nil {
			// No frenamos el flujo por esto — el usuario ya quedó creado y funcional.
			log.Printf("no se pudo generar el link de reset para %s: %v", input.Email, err)
		} else if err := enviarMailInvitacionAdmin(input.Email, input.Nombre, rol, "Elegir mi contraseña", resetLink); err != nil {
			// Tampoco frenamos: la cuenta ya existe y el claim ya está puesto.
			log.Printf("no se pudo enviar el mail de invitación a %s: %v", input.Email, err)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"uid":    userRecord.UID,
			"email":  input.Email,
		})
	}
}

func generarPasswordTemporal() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
