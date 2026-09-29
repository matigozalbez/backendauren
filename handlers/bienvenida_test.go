package handlers

import (
	"strings"
	"testing"
)

// El mail de bienvenida arma el HTML con placeholders (__LINK__, __LOGO__) y
// después los reemplaza. Si alguien edita la plantilla y deja un typo en un
// placeholder, el mail sale pelado o con el link roto sin que se note en el
// build, así que lo chequeamos acá.

func TestHTMLMailBienvenidaNoDejaPlaceholders(t *testing.T) {
	APP_LINK = "https://appauren-alpha.vercel.app/"

	html := htmlMailBienvenida()

	for _, marcador := range []string{"__LINK__", "__LOGO__", "__HOST__"} {
		if strings.Contains(html, marcador) {
			t.Errorf("quedó el placeholder %s sin reemplazar en el mail", marcador)
		}
	}
}

// El texto del pie tiene que salir de APP_LINK. Si queda escrito a mano el
// dominio, el mail muestra "aurenservicios.com.ar" mientras el href apunta a
// Vercel, que es justamente el estado intermedio de ahora.
func TestHTMLMailBienvenidaElPieCoincideConElLink(t *testing.T) {
	APP_LINK = "https://appauren-alpha.vercel.app/"

	html := htmlMailBienvenida()

	if !strings.Contains(html, ">appauren-alpha.vercel.app</a>") {
		t.Error("el pie no muestra el host de APP_LINK")
	}
	if strings.Contains(html, ">aurenservicios.com.ar</a>") {
		t.Error("el pie tiene el dominio fijo Auren, debería tomarlo de APP_LINK")
	}
}

func TestHTMLMailBienvenidaUraElLinkYElLogo(t *testing.T) {
	APP_LINK = "https://aurenservicios.com.ar/"

	html := htmlMailBienvenida()

	// El logo se arma desde APP_LINK, así que tiene que seguir al dominio.
	esperadoLogo := "https://aurenservicios.com.ar/auren-isotipo.png"
	if !strings.Contains(html, esperadoLogo) {
		t.Errorf("el mail no apunta al logo %s", esperadoLogo)
	}

	if !strings.Contains(html, `href="https://aurenservicios.com.ar/"`) {
		t.Error("el botón de la app no apunta al APP_LINK")
	}
}

// El CSS de los mails usa % (width:100%), que rompe fmt.Sprintf. Por eso la
// plantilla se arma con Replace y no con Sprintf: este test falla si alguien
// vuelve a meter un % sin escapar.
func TestHTMLMailBienvenidaNoRompeConPorcentajes(t *testing.T) {
	APP_LINK = "https://appauren-alpha.vercel.app/"

	html := htmlMailBienvenida()

	if !strings.Contains(html, "100%") {
		t.Error("se perdió el width:100%; el CSS llegó mal al mail")
	}
	// Si alguien reintrodujera un % en la plantilla, al imprimir el resultado
	// aparecería %! (MISSING) o %!s(NOVERB). Chequeamos que no haya rastros.
	if strings.Contains(html, "%!") {
		t.Error("hay un verbo de formato sin resolver en el HTML")
	}
}

func TestLogoDeLaAppNormalizaLaBarraFinal(t *testing.T) {
	casos := map[string]string{
		"https://aurenservicios.com.ar/":     "https://aurenservicios.com.ar/auren-isotipo.png",
		"https://aurenservicios.com.ar":      "https://aurenservicios.com.ar/auren-isotipo.png",
		"https://appauren-alpha.vercel.app/": "https://appauren-alpha.vercel.app/auren-isotipo.png",
	}

	for appLink, esperado := range casos {
		APP_LINK = appLink
		if got := logoDeLaApp(); got != esperado {
			t.Errorf("APP_LINK=%q dio %q, se esperaba %q", appLink, got, esperado)
		}
	}
}

// MAIL_FROM cae al valor viejo si no está en .env, para no romper el envío
// mientras se migra el dominio.
func TestMailFromCaeAlValorPorDefecto(t *testing.T) {
	t.Setenv("MAIL_FROM", "")
	// InicializarConfig lee del entorno, así que con MAIL_FROM vacío tiene que
	// dejar el default histórico.
	anterior := MAIL_FROM
	defer func() { MAIL_FROM = anterior }()

	MAIL_FROM = ""
	InicializarConfig()

	if MAIL_FROM != "Auren <admin@formulariosalud.com.ar>" {
		t.Errorf("sin MAIL_FROM en .env quedó %q, se esperaba el valor por defecto", MAIL_FROM)
	}
}
