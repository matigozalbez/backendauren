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

// origenesPermitidos es la lista de orígenes del panel y de la app. Vive acá
// aparte porque la usan dos caminos: los headers de CORS de cada request normal
// y el upgrade a websocket, que no pasa por setCORSHeaders.
var origenesPermitidos = []string{
	"http://localhost:5173",
	"https://choferesunidos.com.ar",
	"http://localhost:5174",
	"http://localhost:4173",
	"https://appauren-alpha.vercel.app", // Sin la barra / al final
}

// origenPermitido dice si el Origin del request viene de un origen conocido.
func origenPermitido(origen string) bool {
	for _, o := range origenesPermitidos {
		if origen == o {
			return true
		}
	}
	return vercelPreviewRegex.MatchString(origen) || vercelPanelRegex.MatchString(origen)
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origen := r.Header.Get("Origin")

	if origenPermitido(origen) {
		w.Header().Set("Access-Control-Allow-Origin", origen)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Secret")
	}
}
