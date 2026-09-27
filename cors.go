package main

import (
	"net/http"
	"regexp"
)

var vercelPreviewRegex = regexp.MustCompile(`^https://choferesunidos-[a-zA-Z0-9\-]+-matiasgozalbez\.vercel\.app$`)

// El panel admin se sirve desde su propio proyecto de Vercel. El grupo
// opcional cubre la URL de producción y también los previews, que Vercel
// genera con un sufijo distinto en cada deploy, así no hay que tocar esto
// cada vez que se despliega.
var vercelPanelRegex = regexp.MustCompile(`^https://paneladminauren-nine(-[a-zA-Z0-9\-]+)?\.vercel\.app$`)

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origen := r.Header.Get("Origin")

	origenesPermitidos := []string{
		"http://localhost:5173",
		"https://choferesunidos.com.ar",
		"http://localhost:5174",
		"http://localhost:4173",
		"https://appauren-alpha.vercel.app", // Sin la barra / al final
	}

	permitido := false
	for _, o := range origenesPermitidos {
		if origen == o {
			permitido = true
			break
		}
	}

	if !permitido && (vercelPreviewRegex.MatchString(origen) || vercelPanelRegex.MatchString(origen)) {
		permitido = true
	}

	if permitido {
		w.Header().Set("Access-Control-Allow-Origin", origen)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Secret")
	}
}
