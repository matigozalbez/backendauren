package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

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