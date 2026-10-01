package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// El hub tiene que repartir un mismo evento a todas las conexiones abiertas, que
// es justamente lo que permite que haya dos paneles mirando a la vez.
func TestHubAuditoriaDifundeATodosLosClientes(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}

	a := &clienteWS{enviar: make(chan []byte, wsBufferEventos), operador: "a@auren.com.ar"}
	b := &clienteWS{enviar: make(chan []byte, wsBufferEventos), operador: "b@auren.com.ar"}
	h.clientes[a] = struct{}{}
	h.clientes[b] = struct{}{}

	h.Difundir(EventoAuditoria{
		ID:            42,
		Accion:        AccionSocioCrear,
		OperadorEmail: "a@auren.com.ar",
		OperadorRol:   "admin",
		Fecha:         time.Now(),
	})

	for _, c := range []*clienteWS{a, b} {
		select {
		case crudo := <-c.enviar:
			var msg struct {
				Tipo   string          `json:"tipo"`
				Evento EventoAuditoria `json:"evento"`
			}
			if err := json.Unmarshal(crudo, &msg); err != nil {
				t.Fatalf("mensaje inválido: %v", err)
			}
			if msg.Tipo != "auditoria" {
				t.Fatalf("tipo inesperado: %q", msg.Tipo)
			}
			if msg.Evento.ID != 42 {
				t.Fatalf("id inesperado para %s: %d", c.operador, msg.Evento.ID)
			}
		default:
			t.Fatalf("%s no recibió el evento", c.operador)
		}
	}
}

// El panel lee el evento en vivo con las mismas claves que el listado HTTP
// (snake_case). Si EventoAuditoria pierde los tags json, Go manda OperadorEmail
// y la fila en vivo muestra "operador desconocido" hasta que se recarga.
func TestHubAuditoriaMandaLasMismasClavesQueElHTTP(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}
	c := &clienteWS{enviar: make(chan []byte, wsBufferEventos)}
	h.clientes[c] = struct{}{}

	h.Difundir(EventoAuditoria{
		ID:            42,
		OperadorEmail: "op@auren.com.ar",
		OperadorRol:   "operador",
		Accion:        AccionSocioCrear,
		Entidad:       "socio",
		EntidadID:     "30120897",
		Fecha:         time.Now(),
	})

	var msg struct {
		Tipo   string         `json:"tipo"`
		Evento map[string]any `json:"evento"`
	}
	if err := json.Unmarshal(<-c.enviar, &msg); err != nil {
		t.Fatalf("mensaje inválido: %v", err)
	}

	for _, clave := range []string{
		"id", "operador_uid", "operador_email", "operador_rol",
		"accion", "entidad", "entidad_id", "detalle", "ip",
		"user_agent", "fecha",
	} {
		if _, ok := msg.Evento[clave]; !ok {
			t.Fatalf("falta la clave %q en el evento en vivo; llegó: %v", clave, msg.Evento)
		}
	}

	if _, ok := msg.Evento["OperadorEmail"]; ok {
		t.Fatal("el payload quedó con nombres de campo de Go en vez de snake_case")
	}
}

// Un cliente trabado no puede frenar a quien audita: el evento se le descarta.
func TestHubAuditoriaNoBloqueaConClienteLLeno(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}

	lleno := &clienteWS{enviar: make(chan []byte, 1), operador: "lleno@auren.com.ar"}
	lleno.enviar <- []byte(`{}`) // el buffer ya está ocupado
	otro := &clienteWS{enviar: make(chan []byte, 2), operador: "otro@auren.com.ar"}
	h.clientes[lleno] = struct{}{}
	h.clientes[otro] = struct{}{}

	hecho := make(chan struct{})
	go func() {
		h.Difundir(EventoAuditoria{ID: 7})
		close(hecho)
	}()

	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("Difundir se bloqueó con un cliente lleno")
	}

	select {
	case <-otro.enviar:
	default:
		t.Fatal("el evento no llegó al cliente sano")
	}
}

// Sin clientes conectados, difundir no debe panicar.
func TestHubAuditoriaSinClientes(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}
	h.Difundir(EventoAuditoria{ID: 1})
}

// El endpoint rechaza un Origin que no está en la lista de cors.go. El upgrade a
// websocket no pasa por setCORSHeaders, así que esta es la única barrera.
func TestWsAuditoriaRechazaOrigenDesconocido(t *testing.T) {
	puede := func(origen string) bool { return origen == "https://panel-adm.auren.com.ar" }

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/ws/admin", nil)
	r.Header.Set("Origin", "https://sitio-raro.example.com")
	// Headers de handshake: sin ellos el upgrader corta antes de mirar el
	// Origin y el test probaría otra cosa.
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	WsAuditoria(puede)(w, r)

	// 403 y no 400: el 400 sería el upgrader diciendo que faltan headers.
	if w.Code != http.StatusForbidden {
		t.Fatalf("esperábase 403, se obtuvo %d", w.Code)
	}
}

// Revocar el acceso tiene que cerrar solo los sockets de esa persona: los
// demás paneles tienen que seguir recibiendo auditoría.
func TestHubAuditoriaCerrarSesionSoloAlRevocado(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}

	revocado := &clienteWS{enviar: make(chan []byte, 1), uid: "uid-1", operador: "fuera@auren.com.ar"}
	otro := &clienteWS{enviar: make(chan []byte, 1), uid: "uid-2", operador: "dentro@auren.com.ar"}
	h.clientes[revocado] = struct{}{}
	h.clientes[otro] = struct{}{}

	if n := h.CerrarSesion("uid-1"); n != 1 {
		t.Fatalf("esperábase 1 conexión cerrada, se cerraron %d", n)
	}

	// El revocado sale del hub, así que Difundir ya no le escribe.
	h.Difundir(EventoAuditoria{ID: 1})

	if _, sigue := h.clientes[revocado]; sigue {
		t.Fatal("el revocado quedó en el hub")
	}
	if _, sigue := h.clientes[otro]; !sigue {
		t.Fatal("se llevó por delante a una conexión que no era del revocado")
	}
	select {
	case <-otro.enviar:
	default:
		t.Fatal("el evento no llegó a la conexión sana")
	}

	// El canal del revocado quedó cerrado.
	select {
	case _, abierto := <-revocado.enviar:
		if abierto {
			t.Fatal("el canal del revocado sigue abierto")
		}
	default:
		t.Fatal("el canal del revocado no llegó a cerrarse")
	}
}

// El mismo socket se cierra por los dos lados a la vez (el cliente se va y
// encima se le revoca). Si cerrar el canal no fuera idempotente, eso entra en
// panic y se cae el proceso.
func TestCerrarCanalEsIdempotente(t *testing.T) {
	c := &clienteWS{enviar: make(chan []byte, 1), uid: "uid-1"}

	c.cerrarCanal(wsCierreRevocado)
	c.cerrarCanal(wsCierreNormal)
	c.cerrarCanal(wsCierreRevocado)
}

// El panel decide cortar sesión mirando el código de cierre, así que revocar
// tiene que dejar 4003 y no el cierre normal.
func TestRevocarDejaElCodigoDeCierre(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}

	revocado := &clienteWS{enviar: make(chan []byte, 1), uid: "uid-1"}
	normal := &clienteWS{enviar: make(chan []byte, 1), uid: "uid-2"}
	h.clientes[revocado] = struct{}{}
	h.clientes[normal] = struct{}{}

	h.CerrarSesion("uid-1")

	if revocado.codigoCierre != wsCierreRevocado {
		t.Fatalf("esperábase %d, quedó %d", wsCierreRevocado, revocado.codigoCierre)
	}

	// El que se va solo tiene que quedar con el cierre normal.
	normal.cerrarCanal(wsCierreNormal)
	if normal.codigoCierre != wsCierreNormal {
		t.Fatalf("esperábase %d, quedó %d", wsCierreNormal, normal.codigoCierre)
	}
}

func TestCerrarSesionSinUidNoHaceNada(t *testing.T) {
	h := &HubAuditoria{clientes: make(map[*clienteWS]struct{})}

	if n := h.CerrarSesion(""); n != 0 {
		t.Fatalf("esperábase 0, se cerraron %d", n)
	}
}

func TestWsAuditoriaRechazaMetodoDistintoDeGet(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/ws/admin", nil)
	r.Header.Set("Origin", "https://panel-adm.auren.com.ar")

	WsAuditoria(func(string) bool { return true })(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("esperábase 405, se obtuvo %d", w.Code)
	}
}
