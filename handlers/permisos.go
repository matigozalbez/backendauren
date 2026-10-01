package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
)

const (
	rolAdmin    = "admin"
	rolOperador = "operador"
)

// rolesValidos son los dos roles que el panel puede asignar. `admins.rol` guarda
// el mismo valor que el claim `role` de Firebase: la columna es para leerlo en
// el panel, el claim es lo que decide el acceso.
var rolesValidos = map[string]bool{
	rolAdmin:    true,
	rolOperador: true,
}

// normalizarRol deja un rol listo para guardarse: minúsculas, sin espacios. Si
// no es uno de los válidos devuelve el default, que es admin.
func normalizarRol(rol string) string {
	rol = strings.ToLower(strings.TrimSpace(rol))
	if !rolesValidos[rol] {
		return rolAdmin
	}
	return rol
}

type OtorgarAdminInput struct {
	Email string `json:"email"`
	Rol   string `json:"rol"`
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

		rol := normalizarRol(input.Rol)

		ctx := context.Background()

		user, err := authClient.GetUserByEmail(ctx, input.Email)
		if err != nil {
			http.Error(w, "no se encontró un usuario con ese email", http.StatusNotFound)
			return
		}

		// SetCustomUserClaims reemplaza el mapa de claims entero, no agrega
		// claves: por eso van admin y role juntos en cada llamada.
		err = authClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
			// admin va en true solo para el admin: un operador entra al panel con
			// admin false y role operador, y RequireOperador lo deja pasar igual.
			// SetCustomUserClaims reemplaza el mapa entero, por eso van los dos.
			"admin": rol == rolAdmin,
			"role":  rol,
		})
		if err != nil {
			http.Error(w, "error asignando el permiso", http.StatusInternalServerError)
			return
		}

		// Otorgar cambia los claims, pero el token que la persona tiene abierto
		// sigue con el rol viejo hasta que expire (una hora). Para que el cambio
		// se note al instante —sobre todo al degradar un admin a operador— se
		// cortan las sesiones: el próximo request da 401, el panel cierra sesión
		// y al volver a entrar el token ya sale con el rol nuevo.
		if err := authClient.RevokeRefreshTokens(ctx, user.UID); err != nil {
			log.Printf("ERROR revocando sesiones de %s: %v", user.UID, err)
			http.Error(w, "permiso asignado, pero no se pudieron cortar las sesiones abiertas", http.StatusInternalServerError)
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
			user.UID, nombre, apellido, input.Email, rol,
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
		if err := enviarMailInvitacionAdmin(input.Email, nombre, rol, "Ingresar al panel", APP_LINK_ADMIN); err != nil {
			log.Printf("OTORGADO sin mail a %s: %v", input.Email, err)
		}

		registrarAuditoria(r, AccionPermisoOtorgar, "permiso", user.UID, map[string]any{
			"email_destino": input.Email,
			"rol":           rol,
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"email":  input.Email,
			"uid":    user.UID,
			"rol":    rol,
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

		// role queda vacío a propósito: un token sin role se asume admin (ver
		// RequireRol). Lo que cierra la puerta es `admin: false`, que el
		// middleware mira antes de evaluar el rol. Blanquear el rol solo, sin
		// el admin en false, dejaría acceso.
		err = authClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
			"admin": false,
			"role":  "",
		})
		if err != nil {
			http.Error(w, "error revocando admin", http.StatusInternalServerError)
			return
		}

		// SetCustomUserClaims no invalida el token que la persona ya tiene
		// abierto: solo cambia los claims para los tokens nuevos. El token viejo
		// sigue diciendo admin:false + role:"operador" y pasa RequireOperador
		// igual, así que sin esto revocar no la saca de la sesión. Lo que corta
		// el token vigente es RevokeRefreshTokens, porque mueve el
		// tokensValidAfterTime que mira VerifyIDTokenAndCheckRevoked: el próximo
		// request da 401 y el panel cierra sesión.
		if err := authClient.RevokeRefreshTokens(ctx, user.UID); err != nil {
			log.Printf("ERROR revocando sesiones de %s: %v", user.UID, err)
			http.Error(w, "acceso revocado, pero no se pudieron cortar las sesiones abiertas", http.StatusInternalServerError)
			return
		}

		// El token se verificó una sola vez, en el handshake. Si esa persona
		// tiene el panel abierto, su websocket sigue conectado y sin esto
		// seguiría recibiendo auditoría después de quedar sin acceso.
		if cerradas := CerrarSesionWS(user.UID); cerradas > 0 {
			log.Printf("REVOCADO %s: %d conexiones de websocket cerradas", input.Email, cerradas)
		}

		// Firebase es la fuente de verdad del acceso; la fila de admins es
		// informativo. Si el borrado falla, el acceso ya quedó revocado igual,
		// pero avisamos y cortamos para que quede en el log.
		if _, err := PGPool.Exec(ctx, `DELETE FROM admins WHERE uid = $1`, user.UID); err != nil {
			log.Printf("ERROR borrando la fila de admins %s: %v", user.UID, err)
			http.Error(w, "acceso revocado, pero falló al borrar el registro en admins", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionPermisoRevocar, "permiso", user.UID, map[string]any{
			"email_destino": input.Email,
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
