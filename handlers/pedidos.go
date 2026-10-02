package handlers

import (
	"context"
	"log"
	"strings"
	"time"

	"firebase.google.com/go/v4/messaging"
)

// PedidoNuevo es el aviso en vivo que recibe el panel cuando un socio crea una
// solicitud. Canal es "turnos" (turnos y estudios) o "servicios" y es lo único
// que el panel necesita para prender el numerito rojo del menú. Tipo es el
// subtipo real ('consulta', 'estudio', 'grua', etc.) por si después se quiere
// mostrar algo más.
type PedidoNuevo struct {
	Canal string `json:"canal"`
	Tipo  string `json:"tipo"`
}

// notificarPedidoNuevo queda como var para que main lo enganche con
// ConectarNotificador, igual que notificarEnVivo: así los handlers de alta no
// tienen que conocer el hub ni FCM.
var notificarPedidoNuevo = func(PedidoNuevo) {}

// NotificarPedidoNuevo avisa al panel que entró una solicitud nueva. La llama
// el alta de turno/estudio (canal "turnos") y la de servicios (canal
// "servicios"), después de que quedó guardada en Postgres.
func NotificarPedidoNuevo(canal, tipo string) {
	notificarPedidoNuevo(PedidoNuevo{Canal: canal, Tipo: tipo})
}

// etiquetaTipo traduce el subtipo crudo a algo mostrable en la notificación.
func etiquetaTipo(canal, tipo string) string {
	t := strings.ToLower(strings.TrimSpace(tipo))
	switch canal {
	case "servicios":
		switch t {
		case "grua", "grúa":
			return "grúa"
		case "sepelios":
			return "servicio sepelial"
		case "domicilio":
			return "visita a domicilio"
		}
	default:
		switch t {
		case "consulta":
			return "consulta"
		case "estudio":
			return "estudio"
		}
	}
	return t
}

// textoPedido arma título y cuerpo de la notificación del panel.
func textoPedido(p PedidoNuevo) (string, string) {
	etiqueta := etiquetaTipo(p.Canal, p.Tipo)
	if p.Canal == "servicios" {
		if etiqueta == "" {
			etiqueta = "servicio"
		}
		return "Nueva solicitud de " + etiqueta, "Entró una solicitud de " + etiqueta + "."
	}
	if etiqueta == "" {
		etiqueta = "turno"
	}
	return "Nueva solicitud de turno", "Entró una solicitud de " + etiqueta + "."
}

// EnviarPushPedidoNuevo manda FCM a los tokens de admins/operadores (tabla
// push_tokens_admins). Corre en goroutine desde ConectarNotificador: no bloquea
// el alta del socio y, si falla, la operación de negocio ya quedó aplicada.
func EnviarPushPedidoNuevo(msgClient *messaging.Client, p PedidoNuevo) {
	if msgClient == nil || PGPool == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := PGPool.Query(ctx, `SELECT uid, token FROM push_tokens_admins WHERE token <> ''`)
	if err != nil {
		log.Printf("[PUSH PEDIDO] error leyendo tokens: %v", err)
		return
	}

	type destino struct {
		uid   string
		token string
	}
	var destinos []destino
	for rows.Next() {
		var d destino
		if err := rows.Scan(&d.uid, &d.token); err == nil && d.token != "" {
			destinos = append(destinos, d)
		}
	}
	rows.Close()

	if len(destinos) == 0 {
		return
	}

	titulo, cuerpo := textoPedido(p)

	ruta := "/panelturnos"
	if p.Canal == "servicios" {
		ruta = "/panelservicios"
	}
	link := strings.TrimRight(APP_LINK_ADMIN, "/") + ruta

	for _, d := range destinos {
		msg := &messaging.Message{
			Token: d.token,
			Webpush: &messaging.WebpushConfig{
				Notification: &messaging.WebpushNotification{
					Title: titulo,
					Body:  cuerpo,
					Icon:  "/auren-isotipo.png",
				},
				FCMOptions: &messaging.WebpushFCMOptions{Link: link},
			},
			Data: map[string]string{"canal": p.Canal, "tipo": p.Tipo},
		}

		if _, err := msgClient.Send(ctx, msg); err != nil {
			// Token muerto (app desinstalada, permiso revocado): lo sacamos
			// para no reintentar en cada pedido.
			if messaging.IsUnregistered(err) {
				if _, delErr := PGPool.Exec(ctx,
					`DELETE FROM push_tokens_admins WHERE uid = $1 AND token = $2`, d.uid, d.token,
				); delErr != nil {
					log.Printf("[PUSH PEDIDO] error borrando token muerto de %s: %v", d.uid, delErr)
				}
				continue
			}
			log.Printf("[PUSH PEDIDO] error enviando a %s: %v", d.uid, err)
		}
	}
}
