package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"aurenbackend/middleware"
)

// RegistrarPushTokenAdmin guarda los tokens FCM de admins/operadores para
// avisarles de pedidos nuevos desde el panel. El uid sale del token verificado
// por RequireOperador (middleware/secret.go), no del body.
//   - GET    -> indica si el usuario tiene algún token registrado
//   - POST   -> guarda/actualiza el token de este dispositivo
//   - DELETE -> elimina el token de este dispositivo (o todos si no viene token)
func RegistrarPushTokenAdmin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet &&
			r.Method != http.MethodPost &&
			r.Method != http.MethodDelete {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		op := middleware.OperadorDesdeContext(r.Context())
		if op == nil || op.UID == "" {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		uid := op.UID

		ctx := context.Background()

		if r.Method == http.MethodGet {
			var existe bool
			if err := PGPool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM push_tokens_admins WHERE uid = $1)`, uid,
			).Scan(&existe); err != nil {
				log.Printf("ERROR consultando push token admin de %s: %v", uid, err)
				http.Error(w, "error consultando el token", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"registrado": existe})
			return
		}

		if r.Method == http.MethodDelete {
			// El panel manda el token del dispositivo; si no viene, borramos
			// todos los del usuario (cierre de sesión).
			var input struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			input.Token = strings.TrimSpace(input.Token)

			var err error
			if input.Token == "" {
				_, err = PGPool.Exec(ctx,
					`DELETE FROM push_tokens_admins WHERE uid = $1`, uid)
			} else {
				_, err = PGPool.Exec(ctx,
					`DELETE FROM push_tokens_admins WHERE uid = $1 AND token = $2`, uid, input.Token)
			}
			if err != nil {
				log.Printf("ERROR eliminando push token admin de %s: %v", uid, err)
				http.Error(w, "error eliminando el token", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		var input struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		input.Token = strings.TrimSpace(input.Token)
		if input.Token == "" {
			http.Error(w, "falta el token", http.StatusBadRequest)
			return
		}

		_, err := PGPool.Exec(ctx, `
			INSERT INTO push_tokens_admins (uid, token)
			VALUES ($1, $2)
			ON CONFLICT (uid, token) DO NOTHING`,
			uid, input.Token,
		)
		if err != nil {
			log.Printf("ERROR guardando push token admin de %s: %v", uid, err)
			http.Error(w, "error guardando el token", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
