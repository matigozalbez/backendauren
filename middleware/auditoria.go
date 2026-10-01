package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// RolAdminDefault es el rol que se registra mientras no exista el custom claim
// `role`. Hoy RequireAdmin solo deja pasar admins (admin: bool), asi que todos
// los operadores auditados son admin.
const RolAdminDefault = "admin"

// Operador identifica a la persona detras de un request autenticado.
type Operador struct {
	UID   string
	Email string
	Rol   string
}

type ctxOperadorKey struct{}

// ConOperador deja el operador en el context para que los handlers lo lean.
func ConOperador(ctx context.Context, op *Operador) context.Context {
	return context.WithValue(ctx, ctxOperadorKey{}, op)
}

// OperadorDesdeContext devuelve nil si el request no paso por RequireAdmin.
func OperadorDesdeContext(ctx context.Context) *Operador {
	op, _ := ctx.Value(ctxOperadorKey{}).(*Operador)
	return op
}

// IPDelRequest saca la IP del cliente. En la VPS hay un proxy adelante, asi que
// X-Forwarded-For es la fuente real y RemoteAddr seria la del proxy.
func IPDelRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
