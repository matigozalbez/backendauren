package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CooldownDNI frena el pedido masivo hacia un mismo DNI: un cooldown de N
// segundos entre cada solicitud.
//
// Se cuenta por DNI y no por IP a propósito. El abuso que se quiere frenar es
// el spam de códigos hacia un socio concreto, y ese abuso es contra un DNI, no
// contra una IP. Contar por IP no sirve porque los móviles en Argentina salen
// por CGNAT (miles de personas detrás de la misma IP pública), así que
// castiga a socios legítimos, y tampoco sirve como defensa porque un atacante
// con varias IP lo esquiva girando.
//
// Se guarda la hora del último request y no un contador. Para "uno cada 60
// segundos" el contador sobra, y además la ventana fija que usaba antes tenía
// un defecto: dos requests pegados justo en el borde de la ventana pasaban los
// dos. Con la hora del último request eso no puede pasar.
//
// El cuerpo se lee y se vuelve a poner para que el handler de fondo pueda
// decodificar el JSON normalmente.
type CooldownDNI struct {
	mu       sync.Mutex
	ultimos  map[string]time.Time
	cooldown time.Duration
}

func NuevoCooldownDNI(cooldown time.Duration) *CooldownDNI {
	return &CooldownDNI{
		ultimos:  make(map[string]time.Time),
		cooldown: cooldown,
	}
}

// cleanup purga las entradas vencidas para que el mapa no crezca sin control.
func (rl *CooldownDNI) cleanup(ahora time.Time) {
	for dni, ultimo := range rl.ultimos {
		if ahora.Sub(ultimo) >= rl.cooldown {
			delete(rl.ultimos, dni)
		}
	}
}

// dniDelRequest normaliza el DNI para que " 30120897 " y "30120897" cuenten
// como el mismo. Si no hay DNI legible, devuelve cadena vacía y la middleware
// deja pasar (el handler se encarga de responder 400).
func dniDelRequest(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	cuerpo, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(strings.NewReader(string(cuerpo)))

	var req struct {
		DNI string `json:"dni"`
	}
	if err := json.Unmarshal(cuerpo, &req); err != nil {
		return ""
	}
	return strings.TrimSpace(req.DNI)
}

func (rl *CooldownDNI) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dni := dniDelRequest(r)

		// Sin DNI legible no hay contra qué contar; que el handler conteste.
		if dni == "" {
			next(w, r)
			return
		}

		ahora := time.Now()

		rl.mu.Lock()
		if len(rl.ultimos) > 5000 {
			rl.cleanup(ahora)
		}
		ultimo, ok := rl.ultimos[dni]
		restante := rl.cooldown - ahora.Sub(ultimo)
		permitido := !ok || restante <= 0
		if permitido {
			rl.ultimos[dni] = ahora
		}
		rl.mu.Unlock()

		if !permitido {
			// Retry-After lleva los segundos que realmente faltan, no la
			// ventana completa, para que el cliente pueda mostrar la espera.
			segundos := int(restante.Seconds())
			if segundos < 1 {
				segundos = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(segundos))
			http.Error(w, "esperá un momento antes de volver a pedir el código", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}
