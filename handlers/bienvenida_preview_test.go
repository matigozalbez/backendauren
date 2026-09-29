package handlers

import (
	"os"
	"testing"
)

// Escriba el mail de bienvenida a un archivo para poder abrirlo en el navegador
// y ver cómo queda. No corre con `go test` normal salvo que se pida el flag:
//
//	go test ./handlers/ -run TestPreviewMailBienvenida -preview-mail
func TestPreviewMailBienvenida(t *testing.T) {
	if os.Getenv("PREVIEW_MAIL") == "" {
		t.Skip("poné PREVIEW_MAIL=1 para generar el HTML de prueba")
	}

	APP_LINK = os.Getenv("APP_LINK_PREVIEW")
	if APP_LINK == "" {
		APP_LINK = "https://appauren-alpha.vercel.app/"
	}

	destino := os.Getenv("PREVIEW_MAIL_DESTINO")
	if destino == "" {
		destino = `C:\Users\matito\AppData\Local\Temp\opencode\mail-bienvenida-preview.html`
	}

	doc := `<!doctype html><html lang="es"><head><meta charset="utf-8">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:wght@600;700&family=Lexend:wght@300;400;500;600;700&display=swap" rel="stylesheet">
</head><body>` + htmlMailBienvenida() + `</body></html>`

	if err := os.WriteFile(destino, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("HTML escrito en", destino)
}
