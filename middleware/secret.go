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

		next(w, r)
	}
}