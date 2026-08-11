package main

import (
	"net/http"
	"regexp"
)

var vercelPreviewRegex = regexp.MustCompile(`^https://choferesunidos-[a-zA-Z0-9\-]+-matiasgozalbez\.vercel\.app$`)

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origen := r.Header.Get("Origin")

	origenesPermitidos := []string{
		"http://localhost:5173",
		"https://choferesunidos.com.ar",
		"https://www.choferesunidos.com.ar",
		"http://localhost:5174",
	}

	permitido := false
	for _, o := range origenesPermitidos {
		if origen == o {
			permitido = true
			break
		}
	}

	if !permitido && vercelPreviewRegex.MatchString(origen) {
		permitido = true
	}

	if permitido {
		w.Header().Set("Access-Control-Allow-Origin", origen)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Secret")
	}
}
