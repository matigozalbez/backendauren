package handlers

import (
	"os"
	"strings"
	"testing"
)

// El mail de una solicitud se arma con placeholders y después los reemplaza.
// Si alguien edita la plantilla y deja un typo, el mail sale con "__TITULO__"
// a la vista y el socio no se entera del turno, sin que se note en el build.

func TestHTMLMailSolicitudNoDejaPlaceholders(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:       "asignado",
		Beneficiario: "Rodrigo",
		Tipo:         "consulta",
		Concepto:     "Cardiología",
		Profesional:  "Dr. Juan Pérez",
		Direccion:    "Calle Falsa 123",
		Fecha:        "2026-09-02",
		Hora:         "10:30",
	})

	for _, marcador := range []string{"__LINK__", "__LOGO__", "__HOST__", "__TITULO__", "__BAJADA__", "__SALUDO__", "__DETALLE__"} {
		if strings.Contains(cuerpo, marcador) {
			t.Errorf("quedó el placeholder %s sin reemplazar en el mail", marcador)
		}
	}
}

func TestHTMLMailSolicitudAsignadoMuestraLosDetalles(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:       "asignado",
		Beneficiario: "Rodrigo",
		Tipo:         "consulta",
		Concepto:     "Cardiología",
		Profesional:  "Dr. Juan Pérez",
		Direccion:    "Calle Falsa 123",
		Fecha:        "2026-09-02",
		Hora:         "10:30",
	})

	for _, esperado := range []string{"Hola Rodrigo,", "Cardiología", "Dr. Juan Pérez", "Calle Falsa 123", "2026-09-02", "10:30"} {
		if !strings.Contains(cuerpo, esperado) {
			t.Errorf("el mail de asignado no muestra %q", esperado)
		}
	}
	if strings.Contains(cuerpo, "no pudo gestionarse") {
		t.Error("el mail de asignado no debería hablar de un rechazo")
	}
}

func TestHTMLMailSolicitudRechazadoMuestraElMotivo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:       "rechazado",
		Beneficiario: "Rodrigo",
		Tipo:         "grua",
		Concepto:     "Remolque",
		Motivo:       "Fuera del radio de cobertura",
	})

	if !strings.Contains(cuerpo, "no pudo gestionarse") {
		t.Error("el mail de rechazo debería decirlo en el título o la bajada")
	}
	if !strings.Contains(cuerpo, "Fuera del radio de cobertura") {
		t.Error("el mail de rechazo no muestra el motivo")
	}
}

// El nombre y el motivo salen de la base y entran directo en el HTML. Sin
// escapar, un "<" rompería el mail entero.
func TestHTMLMailSolicitudEscapaNombreYMotivo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:       "rechazado",
		Beneficiario: `<script>alert(1)</script>`,
		Tipo:         "consulta",
		Motivo:       `<b>inject</b>`,
	})

	if strings.Contains(cuerpo, "<script>") {
		t.Error("el nombre entró sin escapar al HTML del mail")
	}
	if strings.Contains(cuerpo, "<b>inject</b>") {
		t.Error("el motivo entró sin escapar al HTML del mail")
	}
	if !strings.Contains(cuerpo, "&lt;script&gt;") {
		t.Error("el nombre debería quedar escapado con &lt; &gt;")
	}
}

// Sin nombre el saludo no puede quedar "Hola ," dando vueltas.
func TestHTMLMailSolicitudSinNombreNoRompeElSaludo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	for _, nombre := range []string{"", "   "} {
		cuerpo := htmlMailSolicitud(datosMailSolicitud{
			Estado:       "asignado",
			Beneficiario: nombre,
			Tipo:         "consulta",
			Concepto:     "Cardiología",
		})
		if strings.Contains(cuerpo, "Hola  ,") || strings.Contains(cuerpo, "Hola ,") {
			t.Errorf("con nombre %q el saludo quedó colgado", nombre)
		}
		if !strings.Contains(cuerpo, "Hola") {
			t.Errorf("con nombre %q el mail perdió el saludo", nombre)
		}
	}
}

// La grúa es femenina: "la grúa fue asignada", no "asignado".
func TestHTMLMailSolicitudConcuerdaElGeneroDeLaGrua(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:   "asignado",
		Tipo:     "grua",
		Concepto: "Remolque",
	})

	if !strings.Contains(cuerpo, "fue asignada") {
		t.Error("la grúa debería concordar en femenino")
	}
	if strings.Contains(cuerpo, "fue asignado") {
		t.Error("la grúa quedó en masculino")
	}
}

// El logo y el pie se derivan de APP_LINK, igual que en los otros mails.
func TestHTMLMailSolicitudUsaElLogoYElHost(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:   "asignado",
		Tipo:     "consulta",
		Concepto: "Cardiología",
	})

	if !strings.Contains(cuerpo, "https://aurenservicios.com.ar/auren-isotipo.png") {
		t.Error("el mail no apunta al logo derivado de APP_LINK")
	}
	if !strings.Contains(cuerpo, ">aurenservicios.com.ar</a>") {
		t.Error("el pie no muestra el host de APP_LINK")
	}
}

// Escribe el mail a un archivo para abrirlo en el navegador y ver cómo queda,
// sin tocar la base ni mandar nada. No corre con `go test` normal salvo que se
// pida el flag:
//
//	PREVIEW_MAIL=1 go test ./handlers/ -run TestPreviewMailSolicitud -v
func TestPreviewMailSolicitud(t *testing.T) {
	if os.Getenv("PREVIEW_MAIL") == "" {
		t.Skip("poné PREVIEW_MAIL=1 para generar el HTML de prueba")
	}

	APP_LINK = os.Getenv("APP_LINK_PREVIEW")
	if APP_LINK == "" {
		APP_LINK = "https://appauren-alpha.vercel.app/"
	}

	destino := os.Getenv("PREVIEW_MAIL_DESTINO")
	if destino == "" {
		destino = `C:\Users\matito\AppData\Local\Temp\opencode\mail-solicitud-preview.html`
	}

	cuerpo := htmlMailSolicitud(datosMailSolicitud{
		Estado:       "asignado",
		Beneficiario: "Rodrigo",
		Tipo:         "consulta",
		Concepto:     "Cardiología",
		Profesional:  "Dr. Juan Pérez",
		Direccion:    "Calle Falsa 123, Santa Fe",
		Fecha:        "2026-09-02",
		Hora:         "10:30",
	})

	doc := `<!doctype html><html lang="es"><head><meta charset="utf-8">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:wght@600;700&family=Lexend:wght@300;400;500;600;700&display=swap" rel="stylesheet">
</head><body>` + cuerpo + `</body></html>`

	if err := os.WriteFile(destino, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("HTML escrito en", destino)
}
