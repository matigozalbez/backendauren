package middleware

import (
	"context"
	"net/http"
	"strings"
	"aurenbackend/firebase"
)

func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		idToken := strings.TrimPrefix(authHeader, "Bearer ")

		token, err := firebase.AuthClient.VerifyIDToken(context.Background(), idToken)
		if err != nil {
			http.Error(w, "token inválido", http.StatusUnauthorized)
			return
		}

		isAdmin, _ := token.Claims["admin"].(bool)
		if !isAdmin {
			http.Error(w, "no autorizado", http.StatusForbidden)
			return
		}

		// El operador queda en el context para que los handlers puedan auditar
		// la accion. El rol es fijo porque todavia no existe el claim `role`.
		// auth.Token no expone el email: viene en los claims del ID token.
		email, _ := token.Claims["email"].(string)
		operador := &Operador{
			UID:   token.UID,
			Email: email,
			Rol:   RolAdminDefault,
		}

		next(w, r.WithContext(ConOperador(r.Context(), operador)))
	}
}