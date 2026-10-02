package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"aurenbackend/middleware"

	"firebase.google.com/go/v4/messaging"
	"github.com/gorilla/websocket"
)

// Tiempos del websocket de auditoría. El ping mantiene viva la conexión a
// través de proxies y el read deadline es lo que detecta al cliente que se fue
// sin cerrar: sin esto, una pestaña cerrada deja la conexión colgada para
// siempre y el hub acumula clientes muertos.
const (
	wsEsperaEvento    = 20 * time.Second
	wsPongTimeout     = 60 * time.Second
	wsPingInterval    = (wsPongTimeout * 9) / 10
	wsEscribirTimeout = 10 * time.Second
	// wsBufferEventos evita que una conexión lenta frene al que audita. Si se
	// llena, el evento se descarta para esa conexión: no es pérdida real,
	// porque la pantalla también carga por HTTP.
	wsBufferEventos = 32
)

// HubAuditoria reparte eventos entre los clientes conectados. Se usa para el
// canal de auditoría (solo admin) y para el de pedidos (operador y admin): son
// dos instancias separadas, así un operador nunca recibe auditoría. El nombre
// es solo para los logs.
type HubAuditoria struct {
	nombre   string
	mu       sync.RWMutex
	clientes map[*clienteWS]struct{}
}

// clienteWS es una conexión abierta. Es solo-para-lectura: el cliente nunca
// manda nada que cambie estado, así que acá no hay nada que proteger.
//
// El uid va guardado para poder cerrar la sesión de una persona concreta: el
// handshake valida el token una sola vez, así que sin esto revocar el acceso no
// cortaría una conexión ya abierta y esa persona seguiría recibiendo auditoría.
type clienteWS struct {
	conn     *websocket.Conn
	enviar   chan []byte
	uid      string
	operador string
	rol      string

	// El canal se cierra desde dos lados: cuando el cliente se va (en
	// Suscribir) y cuando a esa persona se le revoca el acceso. Sin el sync.Once
	// los dos caminos pueden cerrarlo y eso entra en panic.
	cerrar sync.Once

	// codigoCierre lo escribe quien cierra el canal y lo leerá escribir() para
	// mandarle el close frame al cliente. Se escribe siempre antes de cerrar el
	// canal, así que el receive en escribir() garantiza que ya está visible.
	codigoCierre int
}

// Códigos de cierre propios. Los del rango 4000-4999 son para uso privado, así
// que no chocan con los estándar del RFC 6455.
const (
	wsCierreNormal   = 1000
	wsCierreRevocado = 4003
)

// cierreMotivo es el texto que viaja en el close frame. Va incluido porque el
// panel lo muestra: "tu acceso fue revocado" es más útil que un 4003 suelto.
func cierreMotivo(codigo int) string {
	if codigo == wsCierreRevocado {
		return "tu acceso al panel fue revocado"
	}
	return ""
}

// cerrarCanal cierra el canal una sola vez. No cierra el socket: eso lo hace
// escribir() cuando ve el canal cerrado, al mandar el close frame. Así todas
// las escrituras siguen pasando por la misma goroutine, que es lo que exige
// gorilla (dos escrituras concurrentes a la misma conexión entran en panic).
func (c *clienteWS) cerrarCanal(codigo int) {
	c.cerrar.Do(func() {
		c.codigoCierre = codigo
		close(c.enviar)
	})
}

var hubAuditoria = &HubAuditoria{nombre: "auditoría", clientes: make(map[*clienteWS]struct{})}

// hubPedidos lleva los avisos de solicitudes nuevas a todo el que opera
// turnos/servicios. Va aparte del de auditoría para no filtrarle al operador
// eventos que no le corresponden.
var hubPedidos = &HubAuditoria{nombre: "pedidos", clientes: make(map[*clienteWS]struct{})}

// Suscribir agrega la conexión al hub y se queda en la lectura hasta que el
// cliente se va. La escritura corre en su propia goroutine.
func (h *HubAuditoria) Suscribir(conn *websocket.Conn, operador *middleware.Operador) {
	c := &clienteWS{
		conn:   conn,
		enviar: make(chan []byte, wsBufferEventos),
	}
	if operador != nil {
		c.uid = operador.UID
		c.operador = operador.Email
		c.rol = operador.Rol
	}

	h.mu.Lock()
	h.clientes[c] = struct{}{}
	conectados := len(h.clientes)
	h.mu.Unlock()
	log.Printf("[WS] %s: conectado %s (%s) — %d en vivo", h.nombre, c.operador, c.rol, conectados)

	go c.escribir()

	c.leer() // bloquea hasta que el cliente cierra

	h.mu.Lock()
	delete(h.clientes, c)
	restantes := len(h.clientes)
	h.mu.Unlock()
	// Puede que CerrarSesion ya lo haya cerrado (revocación), por eso va por
	// cerrarCanal y no por un close directo.
	c.cerrarCanal(wsCierreNormal)
	log.Printf("[WS] %s: desconectado %s (%s) — %d en vivo", h.nombre, c.operador, c.rol, restantes)
}

// leer corre el loop de lectura. Las lecturas no sirven para recibir datos sino
// para detectar el cierre y para renormalizar el deadline cuando llega el pong.
// Cuando hay error se cierra el socket, que es lo que corta la escritura.
func (c *clienteWS) leer() {
	defer c.conn.Close()

	c.conn.SetReadLimit(1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// escribir manda los eventos del canal y el ping de keepalive. El close del
// canal (cuando el cliente ya se fue) cierra la conexión de forma limpia.
func (c *clienteWS) escribir() {
	ticker := time.NewTicker(wsPingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case mensaje, ok := <-c.enviar:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsEscribirTimeout))
			if !ok {
				// El canal cerrado es la señal de mandar el close frame. El
				// código le dice al panel por qué: 4003 es que a esa persona se
				// le revocó el acceso, y ahí el panel cierra sesión.
				_ = c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(c.codigoCierre, cierreMotivo(c.codigoCierre)))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, mensaje); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsEscribirTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Difundir manda el evento de auditoría a todos los clientes. No bloquea.
func (h *HubAuditoria) Difundir(ev EventoAuditoria) {
	h.DifundirTipo("auditoria", ev)
}

// DifundirTipo manda un payload cualquiera, etiquetado con el tipo indicado, a
// todos los clientes del hub. No bloquea: si el buffer de una conexión lenta
// está lleno, el evento se descarta para esa conexión (la pantalla igual carga
// por HTTP).
func (h *HubAuditoria) DifundirTipo(tipo string, v any) {
	payload, err := json.Marshal(map[string]any{"tipo": tipo, "evento": v})
	if err != nil {
		log.Printf("[WS] %s: no se pudo serializar el evento: %v", h.nombre, err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clientes {
		select {
		case c.enviar <- payload:
		default:
			log.Printf("[WS] %s: buffer lleno para %s, evento descartado", h.nombre, c.operador)
		}
	}
}

// CerrarSesion cierra las conexiones abiertas de una persona. La llama
// RevocarAdmin: el token se verificó solo en el handshake, así que sin esto
// revocar el acceso cortaría los requests HTTP pero dejaría el socket abierto,
// y esa persona seguiría recibiendo auditoría en vivo.
//
// Las conexiones salen del hub antes de cerrarse para que Difundir no les
// escriba a un canal que ya se cerró.
func (h *HubAuditoria) CerrarSesion(uid string) int {
	if uid == "" {
		return 0
	}

	h.mu.Lock()
	var aCerrar []*clienteWS
	for c := range h.clientes {
		if c.uid == uid {
			aCerrar = append(aCerrar, c)
			delete(h.clientes, c)
		}
	}
	h.mu.Unlock()

	for _, c := range aCerrar {
		log.Printf("[WS] %s: cierre por revocación de %s (%s)", h.nombre, c.operador, c.rol)
		c.cerrarCanal(wsCierreRevocado)
	}

	return len(aCerrar)
}

// wsUpgrader arma el upgrader validando el Origin contra la lista del panel.
func wsUpgrader(origenPermitido func(string) bool) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return origenPermitido(r.Header.Get("Origin")) },
	}
}

// servirWS son las validaciones comunes del handshake.
//
// origenPermitido se recibe por parámetro porque la lista de orígenes vive en
// el package main (cors.go) y el upgrade a websocket no pasa por
// setCORSHeaders: sin validar el Origin acá, el endpoint queda abierto a
// cualquier sitio.
func servirWS(hub *HubAuditoria, origenPermitido func(string) bool) http.HandlerFunc {
	upgrader := wsUpgrader(origenPermitido)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		// El Origin se valida acá y no solo en el CheckOrigin del upgrader, para
		// que un origen desconocido reciba un 403 con mensaje en lugar del 400
		// genérico del handshake. El CheckOrigin queda igual porque es el que
		// evita el race entre validar y hacer el upgrade.
		if !origenPermitido(r.Header.Get("Origin")) {
			http.Error(w, "origen no permitido", http.StatusForbidden)
			return
		}

		// El navegador exige que el servidor confirme uno de los subprotocolos
		// pedidos. El que se devuelve es "bearer", que es el que el panel pasó
		// primero; el token era el segundo valor y no se devuelve nunca.
		responseHeader := http.Header{}
		responseHeader.Set("Sec-WebSocket-Protocol", middleware.WsSubprotocolo)

		conn, err := upgrader.Upgrade(w, r, responseHeader)
		if err != nil {
			// Upgrade ya respondió.
			log.Printf("[WS] %s: upgrade fallido: %v", hub.nombre, err)
			return
		}

		// El middleware ya validó el token y dejó al operador en el context.
		hub.Suscribir(conn, middleware.OperadorDesdeContext(r.Context()))
	}
}

// WsAuditoria sirve GET /api/ws/admin, solo-para-lectura y solo-admin: transmite
// la auditoría, que el operador no puede leer por HTTP.
func WsAuditoria(origenPermitido func(string) bool) http.HandlerFunc {
	return servirWS(hubAuditoria, origenPermitido)
}

// WsPedidos sirve GET /api/ws/pedidos: avisa en vivo de las solicitudes nuevas
// a todo el que opera turnos y servicios (operador y admin).
func WsPedidos(origenPermitido func(string) bool) http.HandlerFunc {
	return servirWS(hubPedidos, origenPermitido)
}

// ConectarNotificador engancha los hubs con los notificadores. Se llama una vez
// desde main, así que los call sites de registrarAuditoria y de las altas no
// cambian. msgClient es el cliente FCM: con él también se manda el push de los
// pedidos, además del aviso en vivo por websocket.
func ConectarNotificador(msgClient *messaging.Client) {
	notificarEnVivo = func(ev EventoAuditoria) {
		hubAuditoria.Difundir(ev)
	}
	notificarPedidoNuevo = func(p PedidoNuevo) {
		hubPedidos.DifundirTipo("pedido", p)
		// En goroutine: el push no debe frenar el alta del socio.
		go EnviarPushPedidoNuevo(msgClient, p)
	}
}

// CerrarSesionWS corta los websockets abiertos de una persona. RevocarAdmin la
// usa para que la revocación también cierre las conexiones en vivo.
func CerrarSesionWS(uid string) int {
	cerrados := hubAuditoria.CerrarSesion(uid)
	cerrados += hubPedidos.CerrarSesion(uid)
	return cerrados
}
