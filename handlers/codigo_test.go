package handlers

import (
	"os"
	"strings"
	"testing"
)

// El mail del código se arma con placeholders y después los reemplaza. Si
// alguien edita la plantilla y deja un typo, el mail sale con "__CODIGO__" a
// la vista y el socio no puede activar su cuenta, sin que se note en el build.

func TestHTMLMailCodigoNoDejaPlaceholders(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailCodigo("Rodrigo", "481902")

	for _, marcador := range []string{"__LINK__", "__LOGO__", "__HOST__", "__CODIGO__", "__SALUDO__"} {
		if strings.Contains(cuerpo, marcador) {
			t.Errorf("quedó el placeholder %s sin reemplazar en el mail", marcador)
		}
	}
}

func TestHTMLMailCodigoPoneElCodigoYElSaludo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailCodigo("Rodrigo", "481902")

	if !strings.Contains(cuerpo, "481902") {
		t.Error("el mail no muestra el código de verificación")
	}
	if !strings.Contains(cuerpo, "Hola Rodrigo,") {
		t.Error("el mail no saluda al socio por nombre")
	}
	if !strings.Contains(cuerpo, "Vence en 10 minutos") {
		t.Error("el mail no avisa el vencimiento, que en el código son 10 minutos de verdad")
	}
}

// El nombre sale de la tabla socios y entra directo en el HTML. Sin escapar,
// un nombre con "<" rompería el mail entero.
func TestHTMLMailCodigoEscapaElNombre(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailCodigo(`<script>alert(1)</script>`, "481902")

	if strings.Contains(cuerpo, "<script>") {
		t.Error("el nombre entró sin escapar al HTML del mail")
	}
	if !strings.Contains(cuerpo, "&lt;script&gt;") {
		t.Error("el nombre debería quedar escapado con &lt; &gt;")
	}
}

// Sin nombre el saludo no puede quedar "Hola ," dando vueltas.
func TestHTMLMailCodigoSinNombreNoRompeElSaludo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	for _, nombre := range []string{"", "   "} {
		cuerpo := htmlMailCodigo(nombre, "481902")
		if strings.Contains(cuerpo, "Hola  ,") || strings.Contains(cuerpo, "Hola ,") {
			t.Errorf("con nombre %q el saludo quedó colgado: %q", nombre, "Hola")
		}
		if !strings.Contains(cuerpo, "Hola") {
			t.Errorf("con nombre %q el mail perdió el saludo", nombre)
		}
	}
}

// El logo se arma desde APP_LINK, igual que en el de bienvenida. Este mail no
// lleva botón (el código se tipea en la app), así que el único destino es el
// link del pie.
func TestHTMLMailCodigoUraElLogoYElLink(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailCodigo("Rodrigo", "481902")

	if !strings.Contains(cuerpo, "https://aurenservicios.com.ar/auren-isotipo.png") {
		t.Error("el mail no apunta al logo derivado de APP_LINK")
	}
	if !strings.Contains(cuerpo, ">aurenservicios.com.ar</a>") {
		t.Error("el pie no muestra el host de APP_LINK")
	}
	if strings.Contains(cuerpo, "Abrir la app") {
		t.Error("quedó el botón de la app, que no corresponde a este mail")
	}
}

// Escribe el mail a un archivo para abrirlo en el navegador y ver cómo queda,
// sin tocar la base ni mandar nada. No corre con `go test` normal salvo que se
// pida el flag:
//
//	PREVIEW_MAIL=1 go test ./handlers/ -run TestPreviewMailCodigo -v
func TestPreviewMailCodigo(t *testing.T) {
	if os.Getenv("PREVIEW_MAIL") == "" {
		t.Skip("poné PREVIEW_MAIL=1 para generar el HTML de prueba")
	}

	APP_LINK = os.Getenv("APP_LINK_PREVIEW")
	if APP_LINK == "" {
		APP_LINK = "https://appauren-alpha.vercel.app/"
	}

	destino := os.Getenv("PREVIEW_MAIL_DESTINO")
	if destino == "" {
		destino = `C:\Users\matito\AppData\Local\Temp\opencode\mail-codigo-preview.html`
	}

	doc := `<!doctype html><html lang="es"><head><meta charset="utf-8">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:wght@600;700&family=Lexend:wght@300;400;500;600;700&display=swap" rel="stylesheet">
</head><body>` + htmlMailCodigo(os.Getenv("PREVIEW_MAIL_NOMBRE"), os.Getenv("PREVIEW_MAIL_CODIGO")) + `</body></html>`

	if err := os.WriteFile(destino, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("HTML escrito en", destino)
}
