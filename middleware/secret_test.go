package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestTokenDelRequestHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/auditoria", nil)
	r.Header.Set("Authorization", "Bearer token-de-header")

	if got := tokenDelRequest(r); got != "token-de-header" {
		t.Fatalf("Authorization: esperábase token-de-header, se obtuvo %q", got)
	}
}

func TestTokenDelRequestWebsocket(t *testing.T) {
	// El handshake del websocket no puede mandar headers: el token va en el
	// subprotocolo, después del nombre.
	r := httptest.NewRequest("GET", "/api/ws/admin", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "bearer, token-de-subprotocolo")

	if got := tokenDelRequest(r); got != "token-de-subprotocolo" {
		t.Fatalf("subprotocolo: esperábase token-de-subprotocolo, se obtuvo %q", got)
	}
}

func TestTokenDelRequestHeaderGanaSobreSubprotocolo(t *testing.T) {
	// Si viene el header, manda el header. El subprotocolo se mira solo cuando
	// no hay Authorization, para no dejar que un valor raro pise al bueno.
	r := httptest.NewRequest("GET", "/api/admin/auditoria", nil)
	r.Header.Set("Authorization", "Bearer token-de-header")
	r.Header.Set("Sec-WebSocket-Protocol", "bearer, token-de-subprotocolo")

	if got := tokenDelRequest(r); got != "token-de-header" {
		t.Fatalf("esperábase token-de-header, se obtuvo %q", got)
	}
}

func TestTokenDelRequestSinNada(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/auditoria", nil)

	if got := tokenDelRequest(r); got != "" {
		t.Fatalf("esperábase token vacío, se obtuvo %q", got)
	}
}

func TestTokenDelRequestSubprotocoloSinBearer(t *testing.T) {
	// Sin el nombre "bearer" no hay forma de saber cuál de los valores es el
	// token, así que no se adivina: se rechaza. Adivinar abriría la puerta a
	// que un cliente mande el subprotocolo que quiera y que se lea un valor
	// distinto al suyo.
	r := httptest.NewRequest("GET", "/api/ws/admin", nil)
	r.Header.Set("Sec-WebSocket-Protocol", "otro, token-cualquiera")

	if got := tokenDelRequest(r); got != "" {
		t.Fatalf("esperábase token vacío, se obtuvo %q", got)
	}
}
