package handlers

import (
	"context"
	"os"
	"strings"
	"testing"
)

// El mail de invitación se arma con placeholders y después los reemplaza. Si
// alguien edita la plantilla y deja un typo, el mail sale con "__BOTON__" a la
// vista y el invitado no tiene por dónde entrar al panel, sin que se note en
// el build.

func TestHTMLMailInvitacionNoDejaPlaceholders(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	for _, marcador := range []string{
		"__LINK__", "__LINKPIE__", "__LOGO__", "__HOSTPIE__",
		"__BOTON__", "__BOTONBLOQUE__", "__SALUDO__", "__ROL__", "__ROLCOMPLETO__",
	} {
		if strings.Contains(cuerpo, marcador) {
			t.Errorf("quedó el placeholder %s sin reemplazar en el mail", marcador)
		}
	}
}

// El rol es lo único que el invitado necesita leer para saber qué le dieron.
// Sale de la tabla admins, y ahí está en crudo ("admin"), no como se escribe.
func TestHTMLMailInvitacionMuestraElRolEnLegible(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	if !strings.Contains(cuerpo, "Administrador") {
		t.Error("el mail no traduce el rol admin a Administrador")
	}
	if strings.Contains(cuerpo, ">admin<") {
		t.Error("el mail muestra la clave cruda del rol en vez de la etiqueta")
	}
}

// Un rol que todavía no está mapeado tiene que aparecer igual: es preferible ver
// la clave rara que ver un mail sin rol.
func TestHTMLMailInvitacionRolDesconocidoSeMuestraComoViene(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin("Rodrigo", "supervisor", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	if !strings.Contains(cuerpo, "supervisor") {
		t.Error("un rol sin mapear desapareció del mail en vez de mostrarse tal cual")
	}
}

// El nombre sale de Firebase o de la tabla socios y entra directo al HTML. Sin
// escapar, un nombre con "<" rompería el mail entero.
func TestHTMLMailInvitacionEscapaElNombre(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin(`<script>alert(1)</script>`, "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	if strings.Contains(cuerpo, "<script>") {
		t.Error("el nombre entró sin escapar al HTML del mail")
	}
	if !strings.Contains(cuerpo, "&lt;script&gt;") {
		t.Error("el nombre debería quedar escapado con &lt; &gt;")
	}
}

// El logo y el pie salen del deploy del panel, no del de la app: si el deploy
// de la app se cae, el mail de invitación sigue teniendo logo.
func TestHTMLMailInvitacionUraElLogoYElPieDelPanel(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	if !strings.Contains(cuerpo, "https://panel-auren.vercel.app/auren-isotipo.png") {
		t.Error("el mail no apunta al logo del deploy del panel")
	}
	if !strings.Contains(cuerpo, ">panel-auren.vercel.app</a>") {
		t.Error("el pie no muestra el host del panel")
	}
}

// Los dos callers pasan textos de botón distintos, con el mismo template. Si
// el texto o el link no entran, el invitado recibe un mail sin destino.
func TestHTMLMailInvitacionCambiaElBoton(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	otorgar := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")
	if !strings.Contains(otorgar, "Ingresar al panel") {
		t.Error("el mail de OtorgarAdmin no trae el botón de entrar al panel")
	}
	if strings.Contains(otorgar, "Elegir mi contraseña") {
		t.Error("el mail de OtorgarAdmin trae el botón de contraseña, que ese usuario no necesita")
	}

	crear := htmlMailInvitacionAdmin("Rodrigo", "admin", "Elegir mi contraseña", "https://ejemplo.firebaseapp.com/action?mode=reset")
	if !strings.Contains(crear, "Elegir mi contraseña") {
		t.Error("el mail de CrearAdmin no trae el botón de elegir contraseña")
	}
	if !strings.Contains(crear, "mode=reset") {
		t.Error("el mail de CrearAdmin no apunta al link de reset")
	}
	if strings.Contains(crear, "Ingresar al panel") {
		t.Error("el mail de CrearAdmin trae el botón del panel, que todavía no sirve")
	}
}

// Sin link no puede haber un href vacío ni un botón mudo: el mail avisa el rol y
// queda, pero sin destino al que ir.
func TestHTMLMailInvitacionSinLinkNoDejaBotonRoto(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	for _, link := range []string{"", "   "} {
		cuerpo := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", link)

		if strings.Contains(cuerpo, "<a href=") && strings.Contains(cuerpo, "Ingresar al panel") {
			t.Errorf("con link %q quedó un botón sin destino", link)
		}
		if !strings.Contains(cuerpo, "Administrador") {
			t.Errorf("con link %q el mail perdió el rol, que es lo único que puede decir", link)
		}
	}
}

// Sin nombre el saludo no puede quedar "Hola ," dando vueltas.
func TestHTMLMailInvitacionSinNombreNoRompeElSaludo(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	for _, nombre := range []string{"", "   "} {
		cuerpo := htmlMailInvitacionAdmin(nombre, "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

		if strings.Contains(cuerpo, "Hola  ,") || strings.Contains(cuerpo, "Hola ,") {
			t.Errorf("con nombre %q el saludo quedó colgado", nombre)
		}
		if !strings.Contains(cuerpo, "Hola") {
			t.Errorf("con nombre %q el mail perdió el saludo", nombre)
		}
	}
}

// El preheader es lo que se ve en la vista previa de la bandeja. Si alguien edita
// la plantilla y lo borra, el mail abre con un texto vacío arriba.
func TestHTMLMailInvitacionTienePreheader(t *testing.T) {
	APP_LINK_ADMIN = "https://panel-auren.vercel.app/"

	cuerpo := htmlMailInvitacionAdmin("Rodrigo", "admin", "Ingresar al panel", "https://panel-auren.vercel.app/login")

	if !strings.Contains(cuerpo, "Fuiste invitado a Auren") {
		t.Error("el mail no tiene preheader, o tiene uno que no dice de qué se trata")
	}
}

// admins.nombre y admins.apellido son NOT NULL, así que nombreDelAdmin no puede
// devolver dos cadenas vacías en ningún caso.
func TestNombreDelAdminConDisplayName(t *testing.T) {
	casos := []struct {
		desc, displayName string
		nombre, apellido  string
	}{
		{"nombre y apellido", "Rodrigo Pérez", "Rodrigo", "Pérez"},
		{"dos nombres", "Juan Carlos Pérez", "Juan", "Carlos Pérez"},
		{"un solo token", "Rodrigo", "Rodrigo", ""},
		{"espacios al borde", "  Rodrigo Pérez  ", "Rodrigo", "Pérez"},
	}

	for _, c := range casos {
		nombre, apellido := nombreDelAdmin(context.Background(), "a@b.com", c.displayName)
		if nombre != c.nombre || apellido != c.apellido {
			t.Errorf("%s: con DisplayName %q dio (%q, %q), esperaba (%q, %q)",
				c.desc, c.displayName, nombre, apellido, c.nombre, c.apellido)
		}
	}
}

// El fallback "(sin nombre)" tiene que existir justamente para el caso sin
// DisplayName, y el email queda en el apellido para que la fila sea rastreable.
func TestNombreDelAdminSinDisplayNameNoDevuelveVacias(t *testing.T) {
	// PGPool es nil en los tests, que es el mismo camino que un socio que no
	// está en la base: no hay de dónde sacar el nombre.
	nombre, apellido := nombreDelAdmin(context.Background(), "socio@ejemplo.com", "")

	if nombre == "" {
		t.Error("nombreDelAdmin devolvió nombre vacío, y admins.nombre es NOT NULL")
	}
	if apellido == "" {
		t.Error("nombreDelAdmin devolvió apellido vacío, y admins.apellido es NOT NULL")
	}
	if apellido != "socio@ejemplo.com" {
		t.Errorf("con nombre %q el apellido debería quedar en el email para rastrear la fila, dio %q", nombre, apellido)
	}
}

func TestEtiquetaRol(t *testing.T) {
	casos := map[string]string{
		"admin":      "Administrador",
		"Admin":      "Administrador",
		" admin ":    "Administrador",
		"operador":   "Operador",
		"":           "Administrador",
		"supervisor": "supervisor",
	}

	for entrada, esperado := range casos {
		if salida := etiquetaRol(entrada); salida != esperado {
			t.Errorf("etiquetaRol(%q) = %q, esperaba %q", entrada, salida, esperado)
		}
	}
}

// Escribe el mail a un archivo para abrirlo en el navegador y ver cómo queda,
// sin tocar la base ni mandar nada. No corre con `go test` normal salvo que se
// pida el flag:
//
//	PREVIEW_MAIL=1 go test ./handlers/ -run TestPreviewMailInvitacion -v
func TestPreviewMailInvitacion(t *testing.T) {
	if os.Getenv("PREVIEW_MAIL") == "" {
		t.Skip("poné PREVIEW_MAIL=1 para generar el HTML de prueba")
	}

	APP_LINK_ADMIN = os.Getenv("APP_LINK_ADMIN_PREVIEW")
	if APP_LINK_ADMIN == "" {
		APP_LINK_ADMIN = "https://panel-auren-alpha.vercel.app/"
	}

	destino := os.Getenv("PREVIEW_MAIL_DESTINO")
	if destino == "" {
		destino = `C:\Users\matito\AppData\Local\Temp\opencode\mail-invitacion-admin-preview.html`
	}

	doc := `<!doctype html><html lang="es"><head><meta charset="utf-8">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:wght@600;700&family=Lexend:wght@300;400;500;600;700&display=swap" rel="stylesheet">
</head><body>` + htmlMailInvitacionAdmin(
		os.Getenv("PREVIEW_MAIL_NOMBRE"), "admin",
		"Ingresar al panel", APP_LINK_ADMIN,
	) + `</body></html>`

	if err := os.WriteFile(destino, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("HTML escrito en", destino)
}
