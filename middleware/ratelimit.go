package middleware

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type dniBucket struct {
	contador int
	ventana  time.Time
}

// RateLimiterDNI limita por DNI con una ventana fija.
//
// Se cuenta por DNI y no por IP a propósito: el abuso que se quiere frenar
// es el spam de códigos hacia un socio concreto, y ese abuso es contra un
// DNI, no contra una IP. Contar por IP no sirve porque los móviles en
// Argentina salen por CGNAT (miles de personas detrás de la misma IP
// pública), así que castiga a socios legítimos, y tampoco sirve como
// defensa porque un atacante con varias IP lo esquiva girando.
//
// El cuerpo se lee y se vuelve a poner para que el handler de fondo pueda
// decodificar el JSON normalmente.
type RateLimiterDNI struct {
	mu      sync.Mutex
	buckets map[string]*dniBucket
	limite  int
	ventana time.Duration
}

func NuevoRateLimiterDNI(limite int, ventana time.Duration) *RateLimiterDNI {
	return &RateLimiterDNI{
		buckets: make(map[string]*dniBucket),
		limite:  limite,
		ventana: ventana,
	}
}

// cleanup purga los buckets vencidos para que el mapa no crezca sin control.
func (rl *RateLimiterDNI) cleanup(ahora time.Time) {
	for dni, b := range rl.buckets {
		if ahora.Sub(b.ventana) > rl.ventana {
			delete(rl.buckets, dni)
		}
	}
}

// dniDelRequest normaliza el DNI para que " 30120897 " y "30120897" cuenten
// como el mismo. Si no hay DNI legible, devuelve cadena vacía y la
//iddleware deja pasar (el handler se encarga de responder 400).
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

func (rl *RateLimiterDNI) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dni := dniDelRequest(r)

		// Sin DNI legible no hay contra qué contar; que el handler conteste.
		if dni == "" {
			next(w, r)
			return
		}

		ahora := time.Now()

		rl.mu.Lock()
		if len(rl.buckets) > 5000 {
			rl.cleanup(ahora)
		}
		b, ok := rl.buckets[dni]
		if !ok {
			b = &dniBucket{ventana: ahora}
			rl.buckets[dni] = b
		}
		if ahora.Sub(b.ventana) >= rl.ventana {
			b.contador = 0
			b.ventana = ahora
		}
		b.contador++
		permitido := b.contador <= rl.limite
		rl.mu.Unlock()

		if !permitido {
			segundos := int(rl.ventana.Seconds())
			w.Header().Set("Retry-After", itoa(segundos))
			http.Error(w, "pediste demasiados códigos, esperá un momento", http.StatusTooManyRequests)
			return
		}

		next(w, r)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ipDe resuelve la IP real del cliente respetando X-Forwarded-For, que es lo
// que Caddy manda cuando el backend escucha solo en localhost. Sin esto,
// r.RemoteAddr es siempre 127.0.0.1 y todas las peticiones caen en el mismo
// bucket. Solo se usa para fines de logging: no debe ser la clave de ningún
// rate limit, porque un cliente puede mandar ese header a voluntad.
func ipDe(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		primera := strings.TrimSpace(strings.Split(xff, ",")[0])
		if primera != "" {
			return primera
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

var _ = ipDe
