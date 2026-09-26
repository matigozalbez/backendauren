package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

// RegistrarPushToken reemplaza la escritura directa que la app hacía a la
// colección Firestore push_tokens. Ahora los tokens viven en PostgreSQL.
//   - GET    -> indica si el uid autenticado tiene un token registrado
//   - POST   -> guarda/actualiza el token del uid autenticado
//   - DELETE -> elimina el token del uid autenticado
func RegistrarPushToken(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet &&
			r.Method != http.MethodPost &&
			r.Method != http.MethodDelete {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()

		if r.Method == http.MethodGet {
			var existe bool
			if err := PGPool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM push_tokens WHERE uid = $1)`, uid,
			).Scan(&existe); err != nil {
				log.Printf("ERROR consultando push token de %s: %v", uid, err)
				http.Error(w, "error consultando el token", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"registrado": existe})
			return
		}

		if r.Method == http.MethodDelete {
			if _, err := PGPool.Exec(ctx,
				`DELETE FROM push_tokens WHERE uid = $1`, uid,
			); err != nil {
				log.Printf("ERROR eliminando push token de %s: %v", uid, err)
				http.Error(w, "error eliminando el token", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		var input struct {
			Token  string   `json:"token"`
			Planes []string `json:"planes,omitempty"`
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

		listaPlanes := input.Planes
		if len(listaPlanes) == 0 {
			// La app ya no manda los planes: los tomamos del socio en PG.
			// Ojo: en "socios" los planes son objetos {nombre, estado}, así que
			// hay que extraer el nombre (push_tokens.planes guarda strings y se
			// consulta con el operador @>).
			var raw []byte
			if err := PGPool.QueryRow(ctx,
				`SELECT COALESCE(planes::text, '[]') FROM socios WHERE uid = $1`, uid,
			).Scan(&raw); err == nil && len(raw) > 0 {
				var planesSocio []struct {
					Nombre string `json:"nombre"`
				}
				if json.Unmarshal(raw, &planesSocio) == nil {
					for _, p := range planesSocio {
						if p.Nombre != "" {
							listaPlanes = append(listaPlanes, p.Nombre)
						}
					}
				}
			}
		}

		planesJSON, err := json.Marshal(listaPlanes)
		if err != nil {
			http.Error(w, "planes inválidos", http.StatusBadRequest)
			return
		}

		_, err = PGPool.Exec(ctx, `
			INSERT INTO push_tokens (uid, token, planes)
			VALUES ($1, $2, $3)
			ON CONFLICT (uid) DO UPDATE SET
				token = EXCLUDED.token,
				planes = EXCLUDED.planes,
				actualizado_en = now()`,
			uid, input.Token, planesJSON,
		)
		if err != nil {
			log.Printf("ERROR guardando push token de %s: %v", uid, err)
			http.Error(w, "error guardando el token", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}