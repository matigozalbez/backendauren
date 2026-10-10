package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"
)

// datosMailSolicitud junta todo lo que necesita el mail de una solicitud
// (turno, estudio o servicio) para armarse. Los campos vacíos se omiten del
// bloque de detalle, así el mismo builder sirve para médico, clínica o grúa.
type datosMailSolicitud struct {
	// Estado nuevo de la solicitud: "asignado" o "rechazado".
	Estado string

	// Destinatario es el correo del socio titular.
	Destinatario string

	// Beneficiario es a quién corresponde la solicitud (titular o adherente).
	// Se usa para el saludo.
	Beneficiario string

	// Tipo es el tipo de solicitud: consulta, estudio, grua, sepelios,
	// domicilio u opticaortopedia.
	Tipo string

	// Concepto es la especialidad (turnos y estudios) o el tipo de servicio
	// elegido (grúa, sepelios). Para médico a domicilio viene vacío.
	Concepto string

	// Profesional es el médico o la clínica. Vacío para los servicios.
	Profesional string

	Direccion string
	Fecha     string
	Hora      string

	// Motivo es el motivo del rechazo. Solo se usa en "rechazado".
	Motivo string
}

// etiquetaFemeninaPorTipo marca las etiquetas femeninas, para concordar
// "asignada" y no "asignado" (la grúa, por ejemplo).
var etiquetaFemeninaPorTipo = map[string]bool{
	"grua":            true,
	"opticaortopedia": true,
}

// etiquetaDeTipo resuelve el nombre legible del tipo. Comparte la tabla con
// los mensajes del socio (turnos.go), para que el mail y el push digan lo
// mismo. Sin coincidencia cae en "solicitud", que nunca queda mal.
func etiquetaDeTipo(tipo string) string {
	if etiqueta, ok := etiquetaPorTipo[tipo]; ok {
		return etiqueta
	}
	return "solicitud"
}

// adjetivoAsignado concuerda el participio con la etiqueta del tipo.
func adjetivoAsignado(tipo string) string {
	if etiquetaFemeninaPorTipo[tipo] {
		return "asignada"
	}
	return "asignado"
}

// tituloMailSolicitud es el h1 del mail según el estado de la solicitud.
func tituloMailSolicitud(d datosMailSolicitud) string {
	etiqueta := etiquetaDeTipo(d.Tipo)
	if d.Estado == "rechazado" {
		return "Tu " + etiqueta + " no pudo gestionarse"
	}
	return "Tu " + etiqueta + " fue " + adjetivoAsignado(d.Tipo)
}

// asuntoMailSolicitud es el subject del correo, mismo criterio que el h1.
func asuntoMailSolicitud(d datosMailSolicitud) string {
	etiqueta := etiquetaDeTipo(d.Tipo)
	if d.Estado == "rechazado" {
		return "Tu " + etiqueta + " no pudo gestionarse - Auren"
	}
	return "Tu " + etiqueta + " fue " + adjetivoAsignado(d.Tipo) + " - Auren"
}

// bajadaMailSolicitud es el párrafo que precede al detalle.
func bajadaMailSolicitud(d datosMailSolicitud) string {
	if d.Estado == "rechazado" {
		return "Revisamos tu solicitud y no pudimos gestionarla. Si querés, podés volver a pedirla desde la app."
	}
	return "Tu solicitud fue asignada correctamente. Estos son los detalles:"
}

// etiquetaConcepto rotula la fila del concepto según el tipo de solicitud.
func etiquetaConcepto(tipo string) string {
	switch tipo {
	case "consulta", "estudio":
		return "Especialidad"
	case "grua", "sepelios", "opticaortopedia":
		return "Tipo de servicio"
	default:
		return "Detalle"
	}
}

// etiquetaProfesional rotula la fila del profesional. Un estudio se deriva a
// una clínica, no a una persona, así que ahí el rótulo cambia.
func etiquetaProfesional(tipo string) string {
	switch tipo {
	case "estudio":
		return "Clínica"
	case "opticaortopedia":
		return "Establecimiento"
	default:
		return "Profesional"
	}
}

// filaDetalle arma una fila del bloque de detalle. Si el valor viene vacío
// devuelve cadena vacía, así el detalle muestra solo lo que existe.
func filaDetalle(etiqueta, valor string) string {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return ""
	}
	return `<p style="margin:0 0 14px 0;font-size:14px;line-height:1.6;color:#4a5568;">` +
		`<strong style="color:#092549;">` + html.EscapeString(etiqueta) + `:</strong><br>` +
		html.EscapeString(valor) + `</p>`
}

// detalleMailSolicitud arma el recuadro con los datos de la solicitud. Las
// filas vacías se descartan. En un rechazo agrega el motivo.
func detalleMailSolicitud(d datosMailSolicitud) string {
	filas := []string{
		filaDetalle(etiquetaConcepto(d.Tipo), d.Concepto),
		filaDetalle(etiquetaProfesional(d.Tipo), d.Profesional),
		filaDetalle("Fecha", d.Fecha),
		filaDetalle("Hora", d.Hora),
		filaDetalle("Dirección", d.Direccion),
	}
	if d.Estado == "rechazado" {
		filas = append(filas, filaDetalle("Motivo", d.Motivo))
	}

	var b strings.Builder
	for _, fila := range filas {
		b.WriteString(fila)
	}
	if strings.TrimSpace(b.String()) == "" {
		return ""
	}
	return `<div style="background-color:#f4f1ea;border-radius:16px;padding:22px 26px;text-align:left;">` +
		b.String() + `</div>`
}

// htmlMailSolicitud arma el HTML del mail de una solicitud (turno, estudio o
// servicio) asignada o rechazada.
//
// Mismo esqueleto que el del código (handlers/afiliadosauth.go), el de
// bienvenida (handlers/bienvenida.go) y el de invitación al panel
// (handlers/mailinvitacionadmin.go): tablas con estilos inline, franja dorada,
// logo y tipografía serif. Todos los lee la misma persona y que se vean
// distintos se nota.
//
// Los % del CSS rompen fmt.Sprintf, así que todo entra por Replace y no por
// formato. El nombre y los datos de la solicitud van escapados: salen de la
// base y entran directo en el HTML.
func htmlMailSolicitud(d datosMailSolicitud) string {
	saludo := "Hola"
	if strings.TrimSpace(d.Beneficiario) != "" {
		saludo = "Hola " + strings.TrimSpace(d.Beneficiario) + ","
	}

	cuerpo := `
<div style="display:none;font-size:1px;color:#ffffff;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;">
	Auren — novedades de tu solicitud.
</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f4f1ea;padding:32px 12px;">
<tr>
<td align="center">

<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background-color:#ffffff;border-radius:20px;overflow:hidden;font-family:Lexend,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;box-shadow:0 4px 20px rgba(9,37,73,0.08);">

	<!-- Franja dorada -->
	<tr>
	<td height="4" style="background-color:#c8a15a;font-size:0;line-height:0;">&nbsp;</td>
	</tr>

	<!-- Logotipo -->
	<tr>
	<td align="center" style="padding:36px 32px 8px 32px;">
		<img src="__LOGO__" width="72" height="72" alt="Auren" style="display:block;width:72px;height:72px;border:0;outline:none;text-decoration:none;">
	</td>
	</tr>

	<!-- Título -->
	<tr>
	<td align="center" style="padding:16px 32px 0 32px;">
		<p style="margin:0 0 10px 0;font-size:10px;letter-spacing:2.5px;text-transform:uppercase;color:#a08148;font-weight:600;">
			Mi Auren
		</p>
		<h1 style="margin:0;font-family:'Libre Baskerville',Georgia,'Times New Roman',serif;font-size:26px;line-height:1.25;color:#092549;font-weight:700;">
			__TITULO__
		</h1>
	</td>
	</tr>

	<!-- Saludo y bajada -->
	<tr>
	<td align="center" style="padding:18px 40px 0 40px;">
		<p style="margin:0 0 8px 0;font-size:15px;line-height:1.7;color:#4a5568;font-weight:300;">
			__SALUDO__
		</p>
		<p style="margin:0;font-size:15px;line-height:1.7;color:#4a5568;font-weight:300;">
			__BAJADA__
		</p>
	</td>
	</tr>

	<!-- Detalle -->
	<tr>
	<td align="center" style="padding:26px 32px 0 32px;">
		__DETALLE__
	</td>
	</tr>

	<!-- Separador -->
	<tr>
	<td align="center" style="padding:32px 40px 0 40px;">
		<div style="height:1px;background-color:rgba(200,161,90,0.30);font-size:0;line-height:0;">&nbsp;</div>
	</td>
	</tr>

	<!-- Pie -->
	<tr>
	<td align="center" style="padding:22px 40px 34px 40px;">
		<p style="margin:0 0 6px 0;font-size:12px;line-height:1.7;color:#64748b;font-weight:300;">
			Auren Servicios
		</p>
		<p style="margin:0;font-size:11px;line-height:1.7;color:#a0aec0;font-weight:300;">
			<a href="__LINK__" style="color:#a08148;text-decoration:underline;">__HOST__</a>
		</p>
	</td>
	</tr>

</table>

</td>
</tr>
</table>
`

	cuerpo = strings.ReplaceAll(cuerpo, "__SALUDO__", html.EscapeString(saludo))
	cuerpo = strings.ReplaceAll(cuerpo, "__TITULO__", html.EscapeString(tituloMailSolicitud(d)))
	cuerpo = strings.ReplaceAll(cuerpo, "__BAJADA__", html.EscapeString(bajadaMailSolicitud(d)))
	cuerpo = strings.ReplaceAll(cuerpo, "__DETALLE__", detalleMailSolicitud(d))
	cuerpo = strings.ReplaceAll(cuerpo, "__LOGO__", logoDeLaApp())
	cuerpo = strings.ReplaceAll(cuerpo, "__HOST__", hostDeLaApp())
	return strings.ReplaceAll(cuerpo, "__LINK__", APP_LINK)
}

// enviarEmailSolicitud manda por Resend el mail de una solicitud (turno,
// estudio o servicio) con la plantilla de Auren. Reemplaza al viejo
// enviarEmailTurnoAsignado, que mandaba HTML plano y un remitente fijo.
func enviarEmailSolicitud(d datosMailSolicitud) error {
	if strings.TrimSpace(d.Destinatario) == "" {
		return fmt.Errorf("mail de solicitud sin destinatario")
	}

	payload := map[string]interface{}{
		"from":    MAIL_FROM,
		"to":      []string{d.Destinatario},
		"subject": asuntoMailSolicitud(d),
		"html":    htmlMailSolicitud(d),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+RESEND_API_KEY)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// Solo el status: ni el body ni el payload, que llevan datos del socio.
		log.Printf("enviarEmailSolicitud: Resend devolvio status %d", resp.StatusCode)
		return fmt.Errorf("resend devolvió status %d", resp.StatusCode)
	}
	return nil
}
