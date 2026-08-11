package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
)

type CrearUsuarioInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	DNI      string `json:"dni"`
}

func CrearUsuario(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input CrearUsuarioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Email == "" || input.Password == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// 1. Validar que el DNI pertenezca a un socio real
		doc, err := fsClient.Collection("socios").Doc(input.DNI).Get(ctx)
		if err != nil || !doc.Exists() {
			http.Error(w, "ese DNI no pertenece a ningún socio", http.StatusNotFound)
			return
		}

		data := doc.Data()
		if data["uid"] != nil {
			http.Error(w, "este DNI ya tiene una cuenta vinculada", http.StatusConflict)
			return
		}

		// 2. Crear el usuario real en Firebase Auth
		user, err := authClient.CreateUser(ctx, (&auth.UserToCreate{}).
			Email(input.Email).
			Password(input.Password),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// 3. Vincular el uid al socio
		_, err = fsClient.Collection("socios").Doc(input.DNI).Update(ctx, []firestore.Update{
			{Path: "uid", Value: user.UID},
		})
		if err != nil {
			http.Error(w, "error vinculando cuenta", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"uid":    user.UID,
		})
	}
}
