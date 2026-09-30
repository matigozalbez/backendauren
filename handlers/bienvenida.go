package handlers

// Bienvenida de nuevos socios.
//
// PG (tabla "socios_conocidos", creada por el administrador de la BD, NO acá)
// es la fuente de verdad para saber quién es "nuevo": un DNI que no aparece
// en la tabla es un socio recién llegado y entra a la cola de bienvenida
// (mail_bienvenida_enviado_at IS NULL).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dnisConocidos devuelve el set de DNIs que ya existen en socios_conocidos.
func dnisConocidos(ctx context.Context, dnis []string) (map[string]bool, error) {
	res := make(map[string]bool)
	if len(dnis) == 0 {
		return res, nil
	}

	rows, err := PGPool.Query(ctx, "SELECT dni FROM socios_conocidos WHERE dni = ANY($1::text[])", dnis)
	if err != nil {
		log.Printf("ERROR consultando socios_conocidos: %v", err)
		return res, err
	}
	defer rows.Close()

	for rows.Next() {
		var dni string
		if err := rows.Scan(&dni); err != nil {
			continue
		}
		res[dni] = true
	}
	return res, nil
}

// insertConocidos hace INSERT ... ON CONFLICT (dni) DO NOTHING por lote.
func insertConocidos(ctx context.Context, columns []string, rows [][]interface{}) (int, error) {
	if len(rows) == 0 || len(columns) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(rows))
	for i := range rows {
		parts := make([]string, 0, len(columns))
		for j := range columns {
			parts = append(parts, fmt.Sprintf("$%d", i*len(columns)+j+1))
		}
		placeholders = append(placeholders, "("+strings.Join(parts, ",")+")")
	}

	args := make([]interface{}, 0, len(rows)*len(columns))
	for _, row := range rows {
		args = append(args, row...)
	}

	query := fmt.Sprintf(
		"INSERT INTO socios_conocidos (%s) VALUES %s ON CONFLICT (dni) DO NOTHING",
		strings.Join(columns, ","),
		strings.Join(placeholders, ","),
	)

	tag, err := PGPool.Exec(ctx, query, args...)
	if err != nil {
		log.Printf("ERROR insertando en socios_conocidos: %v", err)
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// BackfillSociosConocidos registra los socios ya existentes en PostgreSQL como
// "ya vistos" (mail_bienvenida_enviado_at = now()) para que no reciban el mail
// de bienvenida con retroactividad. Se corre UNA sola vez al activar la feature.
func BackfillSociosConocidos() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()
		const pageSize = 500

		registrados := 0
		lastDNI := ""

		for {
			rows, err := PGPool.Query(ctx, `
				SELECT COALESCE(dni,''), COALESCE(email,'')
				FROM socios
				WHERE dni > $1
				ORDER BY dni ASC
				LIMIT $2`, lastDNI, pageSize,
			)
			if err != nil {
				http.Error(w, "error leyendo socios", http.StatusInternalServerError)
				return
			}

			type fila struct {
				dni   string
				email string
			}
			var lista []fila
			for rows.Next() {
				var f fila
				if err := rows.Scan(&f.dni, &f.email); err != nil {
					log.Printf("ERROR scaneando socio en backfill: %v", err)
					continue
				}
				lista = append(lista, f)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				http.Error(w, "error leyendo socios", http.StatusInternalServerError)
				return
			}

			if len(lista) == 0 {
				break
			}

			raw := make([][]interface{}, 0, len(lista))
			for _, f := range lista {
				if f.email != "" {
					raw = append(raw, []interface{}{f.dni, f.email})
				}
			}

			if len(raw) > 0 {
				if n, err := insertConocidosConEnvio(ctx, raw); err != nil {
					http.Error(w, "error registrando socios en PG", http.StatusInternalServerError)
					return
				} else {
					registrados += n
				}
			}

			// lastDNI avanza con TODA la página leída, no solo con las filas que
			// tenían email. Si una página entera venía con email NULL, el loop
			// terminaba acá y los socios siguientes nunca se registraban.
			lastDNI = lista[len(lista)-1].dni
			if len(lista) < pageSize {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ok",
			"registrados": registrados,
		})
	}
}

// insertConocidosConEnvio inserta marcando el mail como ya enviado (backfill).
func insertConocidosConEnvio(ctx context.Context, rows [][]interface{}) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(rows))
	for i := range rows {
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d, now())", i*2+1, i*2+2))
	}

	args := make([]interface{}, 0, len(rows)*2)
	for _, row := range rows {
		args = append(args, row[0], row[1])
	}

	query := "INSERT INTO socios_conocidos (dni, email, mail_bienvenida_enviado_at) VALUES " +
		strings.Join(placeholders, ",") +
		" ON CONFLICT (dni) DO NOTHING"

	tag, err := PGPool.Exec(ctx, query, args...)
	if err != nil {
		log.Printf("ERROR en backfill de socios_conocidos: %v", err)
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnviarBienvenidas procesa la cola de bienvenida por lotes (LIMIT 100).
// Fracasos quedan pendientes para la próxima corrida.
func EnviarBienvenidas() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		rows, err := PGPool.Query(ctx, `
			SELECT COALESCE(dni,''), COALESCE(email,'')
			FROM socios_conocidos
			WHERE mail_bienvenida_enviado_at IS NULL AND email <> ''
			LIMIT 100`)
		if err != nil {
			log.Printf("ERROR leyendo pendientes de bienvenida: %v", err)
			http.Error(w, "error leyendo pendientes", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type pendiente struct {
			dni, email string
		}

		var lista []pendiente
		for rows.Next() {
			var p pendiente
			if err := rows.Scan(&p.dni, &p.email); err != nil {
				log.Printf("ERROR scaneando pendiente de bienvenida: %v", err)
				continue
			}
			lista = append(lista, p)
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR iterando pendientes de bienvenida: %v", err)
			http.Error(w, "error leyendo pendientes", http.StatusInternalServerError)
			return
		}

		enviados, fallidos := 0, 0
		for _, p := range lista {
			if err := enviarMailBienvenida(p.email); err != nil {
				log.Printf("ERROR mail bienvenida a dni %s: %v", p.dni, err)
				fallidos++
				continue
			}

			if _, err := PGPool.Exec(ctx,
				`UPDATE socios_conocidos SET mail_bienvenida_enviado_at = now() WHERE dni = $1`,
				p.dni); err != nil {
				log.Printf("ERROR marcando mail enviado para dni %s: %v", p.dni, err)
				fallidos++
				continue
			}
			enviados++
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "ok",
			"enviados": enviados,
			"fallidos": fallidos,
		})
	}
}

// logoDeLaApp arma la URL pública del isotipo a partir de APP_LINK, así el logo
// sigue solo cuando se cambia el dominio de la app.
func logoDeLaApp() string {
	return isotipoDe(APP_LINK)
}

// isotipoDe arma la URL del isotipo a partir de un link base. El panel admin
// sirve su propia copia en paneladminauren/public, así que el isotipo sale del
// dominio del panel y no del de la app: si el deploy de la app llegara a caerse,
// el mail de invitación al panel sigue teniendo logo.
func isotipoDe(link string) string {
	return strings.TrimSuffix(link, "/") + "/auren-isotipo.png"
}

// hostDeLaApp devuelve el dominio de APP_LINK, que se muestra como texto en el
// pie del mail. Se deriva del link y no está escrito a mano para que el texto
// y el href no se contradigan cuando todavía se sirve desde Vercel.
func hostDeLaApp() string {
	return hostDe(APP_LINK)
}

// hostDe devuelve el dominio de cualquier link, para el pie de los mail. El
// fallback es el dominio de producción: si la env viene vacía o mal formada el
// mail sale igual, con el texto del pie que corresponde al Auren de verdad.
func hostDe(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return "aurenservicios.com.ar"
	}
	return u.Host
}

// htmlMailBienvenida arma el HTML del mail de bienvenida.
//
// Es HTML de email, no una página: va en tablas con estilos inline, porque
// Gmail, Outlook y WhatsApp borran los <style> y las clases. Por eso la fuente
// se declara como stack (Libre Baskerville / Lexend no cargan en el mail, caen
// a Georgia y a la del sistema, que es lo más cerca que se puede).
//
// Los % del CSS rompen fmt.Sprintf, así que el link y el logo entran por
// Replace y no por formato.
func htmlMailBienvenida() string {
	html := `
<div style="display:none;font-size:1px;color:#ffffff;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;">
	Bienvenido a Auren. Descargá la app para acceder con tu DNI.
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

	<!-- Bienvenida -->
	<tr>
	<td align="center" style="padding:16px 32px 0 32px;">
		<p style="margin:0 0 10px 0;font-size:10px;letter-spacing:2.5px;text-transform:uppercase;color:#a08148;font-weight:600;">
			Mi Auren
		</p>
		<h1 style="margin:0;font-family:'Libre Baskerville',Georgia,'Times New Roman',serif;font-size:28px;line-height:1.25;color:#092549;font-weight:700;">
			Tu afiliación está completa
		</h1>
	</td>
	</tr>

	<!-- Bajada -->
	<tr>
	<td align="center" style="padding:18px 40px 0 40px;">
		<p style="margin:0;font-size:15px;line-height:1.7;color:#4a5568;font-weight:300;">
			Ya sos parte de Auren. Bajá la app para acceder a tus turnos, estudios y servicios con tu DNI.
		</p>
	</td>
	</tr>

	<!-- Botón -->
	<tr>
	<td align="center" style="padding:32px 32px 8px 32px;">
		<a href="__LINK__" style="display:inline-block;background-color:#092549;color:#ffffff;font-family:Lexend,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:13px;font-weight:600;letter-spacing:1.5px;text-transform:uppercase;text-decoration:none;padding:16px 40px;border-radius:999px;">
			Descargar la app
		</a>
	</td>
	</tr>

	<!-- Link plano por si el botón no se ve -->
	<tr>
	<td align="center" style="padding:16px 32px 0 32px;">
		<p style="margin:0;font-size:11px;line-height:1.6;color:#94a3b8;">
			Si el botón no funciona, copiá esta dirección:<br>
			<a href="__LINK__" style="color:#a08148;text-decoration:underline;">__LINK__</a>
		</p>
	</td>
	</tr>

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
			<a href="__LINK__" style="color:#a08148;text-decoration:underline;">__HOST__</a>
		</p>
	</td>
	</tr>

</table>

</td>
</tr>
</table>
`

	html = strings.ReplaceAll(html, "__LOGO__", logoDeLaApp())
	html = strings.ReplaceAll(html, "__HOST__", hostDeLaApp())
	return strings.ReplaceAll(html, "__LINK__", APP_LINK)
}

func enviarMailBienvenida(to string) error {
	if APP_LINK == "" {
		return fmt.Errorf("APP_LINK no configurada en .env")
	}

	payload := map[string]interface{}{
		"from":    MAIL_FROM,
		"to":      []string{to},
		"subject": "Tu afiliación a Auren está completa",
		"html":    htmlMailBienvenida(),
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
		return fmt.Errorf("resend devolvió status %d", resp.StatusCode)
	}
	return nil
}
