package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// htmlMailInvitacionAdmin arma el HTML del mail con el que se avisa que alguien
// recibió acceso al Panel Admin de Auren.
//
// Mismo esqueleto que el del código (handlers/afiliadosauth.go) y el de
// bienvenida (handlers/bienvenida.go): tablas con estilos inline, franja dorada,
// logo, tipografía serif. Los tres los lee la misma persona en la misma sesión
// y que se vean distintos se nota.
//
// Los % del CSS rompen fmt.Sprintf, así que todo entra por Replace y no por
// formato. El nombre y el rol van escapados: salen de Firebase y de la tabla
// admins, y entran directo en el HTML.
func htmlMailInvitacionAdmin(nombre, rol, textoBoton, linkBoton string) string {
	saludo := "Hola"
	if strings.TrimSpace(nombre) != "" {
		saludo = "Hola " + strings.TrimSpace(nombre) + ","
	}

	// Sin destino no hay botón: el mail avisa igual, pero sin enlace. Pasa
	// cuando APP_LINK_ADMIN no está seteada y la invitation se manda sin link.
	boton := ""
	if strings.TrimSpace(linkBoton) != "" && strings.TrimSpace(textoBoton) != "" {
		boton = `
	<!-- Botón -->
	<tr>
		<td align="center" style="padding:32px 32px 8px 32px;">
			<a href="__LINK__" style="display:inline-block;background-color:#092549;color:#ffffff;font-family:Lexend,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:13px;font-weight:600;letter-spacing:1.5px;text-transform:uppercase;text-decoration:none;padding:16px 40px;border-radius:999px;">
				__BOTON__
			</a>
		</td>
	</tr>

	<!-- Link plano por si el botón no se ve -->
	<tr>
		<td align="center" style="padding:16px 40px 0 40px;">
			<p style="margin:0;font-size:11px;line-height:1.6;color:#94a3b8;">
				Si el botón no funciona, copiá esta dirección:<br>
				<a href="__LINK__" style="color:#a08148;text-decoration:underline;">__LINK__</a>
			</p>
		</td>
	</tr>

	<!-- Aviso -->
	<tr>
		<td align="center" style="padding:14px 40px 0 40px;">
			<p style="margin:0;font-size:11px;line-height:1.6;color:#94a3b8;">
				Este enlace es personal, no lo compartas.
			</p>
		</td>
	</tr>
`
	}

	rolLegible := etiquetaRol(rol)

	cuerpo := `
<div style="display:none;font-size:1px;color:#ffffff;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;">
	Fuiste invitado a Auren. Ingresá al panel con tu email.
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

	<!-- Saludo -->
	<tr>
	<td align="center" style="padding:16px 32px 0 32px;">
		<p style="margin:0 0 10px 0;font-size:10px;letter-spacing:2.5px;text-transform:uppercase;color:#a08148;font-weight:600;">
			Auren Staff
		</p>
		<h1 style="margin:0;font-family:'Libre Baskerville',Georgia,'Times New Roman',serif;font-size:28px;line-height:1.25;color:#092549;font-weight:700;">
			Fuiste invitado al proyecto Auren
		</h1>
	</td>
	</tr>

	<!-- Bajada -->
	<tr>
	<td align="center" style="padding:18px 40px 0 40px;">
		<p style="margin:0;font-size:15px;line-height:1.7;color:#4a5568;font-weight:300;">
			__SALUDO__ Tenés acceso al Panel Admin de Auren, con el rol de <strong>__ROL__</strong>.
		</p>
	</td>
	</tr>

	<!-- Rol -->
	<tr>
	<td align="center" style="padding:28px 32px 0 32px;">
		<div style="display:inline-block;background-color:#f4f1ea;border:1px solid #c8a15a;border-radius:16px;padding:18px 36px;">
			<span style="font-family:Lexend,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:20px;line-height:1.2;letter-spacing:1.5px;font-weight:700;color:#092549;">__ROLCOMPLETO__</span>
		</div>
	</td>
	</tr>
__BOTONBLOQUE__
	<!-- Separador -->
	<tr>
	<td align="center" style="padding:36px 40px 0 40px;">
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
			<a href="__LINKPIE__" style="color:#a08148;text-decoration:underline;">__HOSTPIE__</a>
		</p>
	</td>
	</tr>

</table>

</td>
</tr>
</table>
`

	cuerpo = strings.ReplaceAll(cuerpo, "__BOTONBLOQUE__", boton)
	cuerpo = strings.ReplaceAll(cuerpo, "__SALUDO__", html.EscapeString(saludo))
	cuerpo = strings.ReplaceAll(cuerpo, "__ROLCOMPLETO__", html.EscapeString(rolLegible))
	cuerpo = strings.ReplaceAll(cuerpo, "__ROL__", html.EscapeString(strings.ToLower(rolLegible)))
	cuerpo = strings.ReplaceAll(cuerpo, "__LOGO__", isotipoDe(APP_LINK_ADMIN))
	cuerpo = strings.ReplaceAll(cuerpo, "__HOSTPIE__", hostDe(APP_LINK_ADMIN))
	cuerpo = strings.ReplaceAll(cuerpo, "__LINKPIE__", html.EscapeString(APP_LINK_ADMIN))

	if boton == "" {
		return cuerpo
	}

	cuerpo = strings.ReplaceAll(cuerpo, "__BOTON__", html.EscapeString(textoBoton))
	return strings.ReplaceAll(cuerpo, "__LINK__", html.EscapeString(linkBoton))
}

// etiquetaRol traduce la clave de rol de la tabla admins a algo que se pueda
// leer en un mail. Si aparece un rol nuevo sin mapear, se muestra la clave tal
// cual: es preferible ver "operador" raro a no ver nada.
func etiquetaRol(rol string) string {
	switch strings.ToLower(strings.TrimSpace(rol)) {
	case "admin":
		return "Administrador"
	case "operador":
		return "Operador"
	case "":
		return "Administrador"
	default:
		return strings.TrimSpace(rol)
	}
}

// nombreDelAdmin arma el nombre y el apellido que exige admins.nombre y
// admins.apellido, que son NOT NULL sin default.
//
// Primero se usa el DisplayName de Firebase. El caso normal es el bueno: los
// socios que se registran por la app lo tienen cargado (handlers/afiliadosauth.go,
// DisplayName con nombre y apellido), y los que se crean desde el panel
// también. Cuando hay varios tokens, el primero es el nombre y el resto es el
// apellido, así "Juan Carlos Pérez" no pierde el segundo.
//
// Si el usuario no tiene DisplayName, casi siempre es un socio que ya estaba en
// la base: se busca por email. Si tampoco aparece, se deja un marcador y el
// email como apellido, para que la fila quede rastreable en lugar de romper el
// insert por una columna NOT NULL.
func nombreDelAdmin(ctx context.Context, email, displayName string) (string, string) {
	partes := strings.Fields(displayName)
	if len(partes) > 0 {
		return partes[0], strings.Join(partes[1:], " ")
	}

	if PGPool != nil {
		var nombre, apellido string
		err := PGPool.QueryRow(ctx,
			`SELECT nombre, apellido FROM socios WHERE email = $1`, email,
		).Scan(&nombre, &apellido)
		if err == nil && strings.TrimSpace(nombre) != "" {
			if strings.TrimSpace(apellido) == "" {
				apellido = email
			}
			return nombre, apellido
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("nombreDelAdmin: no se pudo leer el socio de %s: %v", email, err)
		}
	}

	return "(sin nombre)", email
}

// enviarMailInvitacionAdmin manda el mail de invitación al panel.
//
// textoBoton y linkBoton cambian según el caso: CrearAdmin manda el link de
// reset de contraseña ("Elegir mi contraseña") y OtorgarAdmin manda la entrada al
// panel ("Ingresar al panel"), porque ese usuario ya tiene contraseña y no
// necesita volver a elegirla.
func enviarMailInvitacionAdmin(to, nombre, rol, textoBoton, linkBoton string) error {
	if strings.TrimSpace(APP_LINK_ADMIN) == "" {
		return fmt.Errorf("APP_LINK_ADMIN no configurada en .env")
	}

	payload := map[string]interface{}{
		"from":    MAIL_FROM_ADMIN,
		"to":      []string{to},
		"subject": "Fuiste invitado a Auren",
		"html":    htmlMailInvitacionAdmin(nombre, rol, textoBoton, linkBoton),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+RESEND_API_KEY)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// Solo el status: ni el body ni el payload, porque en el caso de
		// CrearAdmin el link es el de reset de contraseña.
		log.Printf("enviarMailInvitacionAdmin: Resend devolvio status %d", resp.StatusCode)
		return fmt.Errorf("resend devolvió status %d", resp.StatusCode)
	}
	return nil
}
