package middleware

import (
	"context"
	"net/http"
	"strings"

	"aurenbackend/firebase"
)

// Roles que RequireRol acepta como argumento.
const (
	RolAdmin    = "admin"
	RolOperador = "operador"
)

// WsSubprotocolo es el nombre del subprotocolo que el panel pide en el
// handshake: "Sec-WebSocket-Protocol: bearer, <idToken>". Lo exporta el
// handler del websocket porque tiene que devolverlo confirmado.
const WsSubprotocolo = "bearer"

// tokenDelRequest saca el ID token del request, del header Authorization o del
// subprotocolo del websocket. Devuelve "" si no hay ninguno.
func tokenDelRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}

	// Sec-WebSocket-Protocol: "bearer, eyJhbGciOi...". El token es el valor
	// que va justo después del nombre del subprotocolo. El navegador exige que
	// el servidor confirme uno de los pedidos, así que el que se devuelve
	// confirmado es "bearer", nunca el token.
	partes := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	for i, p := range partes {
		if strings.TrimSpace(p) != WsSubprotocolo || i+1 >= len(partes) {
			continue
		}
		return strings.TrimSpace(partes[i+1])
	}

	// Sin el nombre del subprotocolo no hay contrato sobre cuál es el token, y
	// adivinar cuál se devuelve sería abrir una puerta de atrás. Quien venga
	// con otra cosa no pasa.
	return ""
}

// claimsDelToken guarda lo que RequireRol necesita del token verificado.
type claimsDelToken struct {
	uid   string
	email string
	rol   string
	admin bool
}

// autenticar valida el token y devuelve los claims. Escribe la respuesta 401 y
// devuelve false si no hay token o si no verifica.
//
// El token llega de dos maneras: por el header Authorization en los requests
// HTTP normales, y por el subprotocolo Sec-WebSocket-Protocol en el handshake
// del websocket, porque el navegador no deja mandar headers en ese handshake.
// El subprotocolo tiene la forma "bearer, <idToken>".
func autenticar(w http.ResponseWriter, r *http.Request) (*claimsDelToken, bool) {
	idToken := tokenDelRequest(r)
	if idToken == "" {
		http.Error(w, "no autorizado", http.StatusUnauthorized)
		return nil, false
	}

	// VerifyIDTokenAndCheckRevoked, y no VerifyIDToken: la diferencia es que
	// verifica contra tokens revocados y cuentas deshabilitadas, no solo la
	// firma. Sin esto, revocar un acceso tarda hasta una hora en surtir
	// efecto, porque el ID token del navegador es una foto vieja que sigue
	// diciendo admin hasta que expira.
	//
	// El precio es un GetUser a Firebase por request (VerifyIDToken normal no
	// hace RPC: cachea las claves públicas). Es aceptable porque estas rutas
	// son las del panel, no las públicas de los socios.
	token, err := firebase.AuthClient.VerifyIDTokenAndCheckRevoked(context.Background(), idToken)
	if err != nil {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return nil, false
	}

	rol, _ := token.Claims["role"].(string)
	rol = strings.ToLower(strings.TrimSpace(rol))
	if rol == "" {
		// Sin claim `role` se asume admin, a propósito: así ningún admin que
		// ya existía antes de este cambio queda afuera del panel el día que se
		// despliegue.
		rol = RolAdminDefault
	}
	admin, _ := token.Claims["admin"].(bool)

	// auth.Token no expone el email: viene en los claims del ID token.
	email, _ := token.Claims["email"].(string)

	return &claimsDelToken{uid: token.UID, email: email, rol: rol, admin: admin}, true
}

// RequireRol deja pasar solo a los operadores cuyo rol está en la lista.
//
// Lo que decide es el claim `role`. El claim `admin: bool` queda como segunda
// lectura, para el token que se contradice a sí mismo (`role: admin` junto a
// `admin: false`), que se trata como no autorizado. Un operador entra con
// `admin: false` y `role: "operador"`, que es lo que escriben OtorgarAdmin y
// CrearAdmin.
func RequireRol(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, ok := autenticar(w, r)
			if !ok {
				return
			}

			if !claims.admin && claims.rol == RolAdmin {
				http.Error(w, "no autorizado", http.StatusForbidden)
				return
			}

			permitido := false
			for _, rolPermitido := range roles {
				if claims.rol == rolPermitido {
					permitido = true
					break
				}
			}
			if !permitido {
				http.Error(w, "no autorizado", http.StatusForbidden)
				return
			}

			// El operador queda en el context para que los handlers puedan
			// auditar la acción con el rol real del token.
			operador := &Operador{
				UID:   claims.uid,
				Email: claims.email,
				Rol:   claims.rol,
			}

			next(w, r.WithContext(ConOperador(r.Context(), operador)))
		}
	}
}

// RequireAdmin es el atajo de RequireRol para lo que queda solo en manos de un
// admin: auditoría, permisos, logs y métricas del servidor.
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return RequireRol(RolAdmin)(next)
}

// RequireOperador es todo lo operativo: socios, turnos, estudios, servicios,
// médicos, clínicas y notificaciones. Entra el operador, un usuario común no.
func RequireOperador(next http.HandlerFunc) http.HandlerFunc {
	return RequireRol(RolAdmin, RolOperador)(next)
}
