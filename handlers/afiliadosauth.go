package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	firebaseauth "firebase.google.com/go/v4/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	AuthClient *firebaseauth.Client
	PGPool     *pgxpool.Pool
)

var RESEND_API_KEY string
var APP_LINK string

func InicializarConfig() {
	RESEND_API_KEY = os.Getenv("RESEND_API_KEY")
	if RESEND_API_KEY == "" {
		log.Println("ADVERTENCIA: RESEND_API_KEY no está seteada")
	}
	APP_LINK = os.Getenv("APP_LINK")
	if APP_LINK == "" {
		log.Println("ADVERTENCIA: APP_LINK no está seteada")
	}
}

// son estructuras =>

type AfiliadoPreRegistrado struct {
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	DNI      string `json:"dni"`
	Email    string `json:"email"`
	Plan     string `json:"plan"`
	UID      string `json:"uid"`
}

type CodigoVerificacion struct {
	Codigo     string    `json:"codigo"`
	Expira     time.Time `json:"expira"`
	Intentos   int       `json:"intentos"`
	Verificado bool      `json:"verificado"`
}

func generarCodigo() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(999999))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// oculta el correo con conversion "matias@gmail.com" en "m****@gmail.com"
func enmascararMail(mail string) string {
	partes := strings.Split(mail, "@")
	if len(partes) != 2 || len(partes[0]) == 0 {
		return "***@***"
	}
	usuario := partes[0]
	if len(usuario) <= 1 {
		return usuario + "***@" + partes[1]
	}
	return string(usuario[0]) + "****@" + partes[1]
}

func enviarCodigoPorMail(destinatario, nombre, codigo string) error {
	log.Printf("DEBUG: intentando enviar código a destinatario=%q nombre=%q", destinatario, nombre)

	payload := map[string]interface{}{
		"from":    "Auren <admin@formulariosalud.com.ar>",
		"to":      []string{destinatario},
		"subject": "Tu código de verificación Auren",
		"html": fmt.Sprintf(
			"<p>Hola %s,</p><p>Tu código para activar tu cuenta es:</p><h2>%s</h2><p>Vence en 10 minutos.</p>",
			nombre, codigo,
		),
	}
	jsonData, _ := json.Marshal(payload)
	log.Printf("DEBUG: payload enviado a Resend: %s", string(jsonData))

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+RESEND_API_KEY)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("DEBUG: error de red pegándole a Resend: %v", err)
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("DEBUG: Resend respondió status=%d body=%s", resp.StatusCode, string(body))

	if resp.StatusCode >= 300 {
		return fmt.Errorf("resend devolvió status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ---------- Endpoint 1: solicitar código ----------

func SolicitarCodigo(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DNI   string `json:"dni"`
		Flujo string `json:"flujo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[solicitar-codigo] JSON invalido: %v", err)
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}
	log.Printf("[solicitar-codigo] request recibido dni=%s flujo=%s", req.DNI, req.Flujo)

	ctx := context.Background()
	respuestaGenerica := map[string]string{"mensaje": "Si el DNI está registrado, te enviamos un código."}

	socio, err := leerSocioPorDNI(ctx, req.DNI)
	if err != nil {
		log.Printf("[solicitar-codigo] DNI %s no encontrado (err=%v)", req.DNI, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respuestaGenerica)
		return
	}
	log.Printf("[solicitar-codigo] socio encontrado para dni=%s", req.DNI)

	afiliadoUID := socio.UIDStr()
	log.Printf("[solicitar-codigo] afiliado: email=%q uid=%q", socio.Email, afiliadoUID)

	if req.Flujo == "primer_ingreso" && afiliadoUID != "" {
		log.Printf("[solicitar-codigo] rechazo: primer_ingreso pero ya tiene UID")
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}
	if req.Flujo == "recuperar_password" && afiliadoUID == "" {
		log.Printf("[solicitar-codigo] rechazo: recuperar_password pero no tiene UID")
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}

	codigo, err := generarCodigo()
	if err != nil {
		log.Printf("[solicitar-codigo] ERROR generando codigo: %v", err)
		http.Error(w, "Error generando código", 500)
		return
	}
	log.Printf("[solicitar-codigo] codigo generado ok")

	// El código anterior se reemplaza, pero `intentos` NO se reinicia al
	// pedir un código nuevo: es un contador acumulado por DNI. Si se
	// reiniciara acá, el límite de 5 intentos de VerificarCodigo se evadiría
	// sin más pidiendo un código y volviendo a fallar 5 veces, en bucle.
	//
	// El reset es por tiempo, no por pedido: si el registro tiene más de una
	// hora, vuelve a cero. Así un socio que se equivocó 5 veces se recupera
	// solo, y aun así el brute force no cierra: con el rate limit de 3
	// códigos/hora quedan 5 intentos por hora contra un código de 6 dígitos.
	_, err = PGPool.Exec(ctx, `
		INSERT INTO codigos_verificacion (dni, codigo, expira, intentos, verificado, creado_en)
		VALUES ($1, $2, $3, 0, FALSE, now())
		ON CONFLICT (dni) DO UPDATE SET
			codigo = EXCLUDED.codigo,
			expira = EXCLUDED.expira,
			verificado = FALSE,
			creado_en = now(),
			intentos = CASE
				WHEN codigos_verificacion.creado_en < now() - interval '1 hour' THEN 0
				ELSE codigos_verificacion.intentos
			END`,
		req.DNI, codigo, time.Now().Add(10*time.Minute),
	)
	if err != nil {
		log.Printf("[solicitar-codigo] ERROR guardando codigo en PG: %v", err)
		http.Error(w, "Error guardando código", 500)
		return
	}
	log.Printf("[solicitar-codigo] codigo guardado en PostgreSQL ok")

	if err := enviarCodigoPorMail(socio.Email, socio.Nombre, codigo); err != nil {
		log.Printf("[solicitar-codigo] ERROR enviando mail a %q: %v", socio.Email, err)
		http.Error(w, "Error enviando el código", 500)
		return
	}
	log.Printf("[solicitar-codigo] mail enviado ok a %q", socio.Email)

	respuestaGenerica["mailEnmascarado"] = enmascararMail(socio.Email)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(respuestaGenerica)
}

// ---------- Endpoint 2: verificar código ----------

func VerificarCodigo(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DNI    string `json:"dni"`
		Codigo string `json:"codigo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	var cv CodigoVerificacion
	err := PGPool.QueryRow(ctx, `
		SELECT codigo, expira, intentos, verificado
		FROM codigos_verificacion WHERE dni = $1`, req.DNI,
	).Scan(&cv.Codigo, &cv.Expira, &cv.Intentos, &cv.Verificado)
	if err != nil {
		http.Error(w, "Código no encontrado, solicitalo de nuevo", http.StatusBadRequest)
		return
	}

	// El contador es acumulado por DNI, no por código: pedir uno nuevo no lo
	// reinicia, así que el mensaje dice que esperen, no que reintenten.
	if cv.Intentos >= 5 {
		http.Error(w, "Demasiados intentos, esperá una hora para volver a intentar", http.StatusTooManyRequests)
		return
	}

	if time.Now().After(cv.Expira) {
		http.Error(w, "El código venció, solicitá uno nuevo", http.StatusBadRequest)
		return
	}

	if cv.Codigo != req.Codigo {
		PGPool.Exec(ctx, `UPDATE codigos_verificacion SET intentos = intentos + 1 WHERE dni = $1`, req.DNI)
		http.Error(w, "Código incorrecto", http.StatusBadRequest)
		return
	}

	PGPool.Exec(ctx, `UPDATE codigos_verificacion SET verificado = TRUE WHERE dni = $1`, req.DNI)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"verificado": true})
}

// ---------- Endpoint 3: crear password / cuenta ----------

func CrearPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DNI      string `json:"dni"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}
	if req.DNI == "" || req.Password == "" {
		http.Error(w, "faltan datos", http.StatusBadRequest)
		return
	}

	if !reDNI.MatchString(req.DNI) {
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}

	if len(req.Password) < 8 {
		http.Error(w, "La contraseña debe tener al menos 8 caracteres", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Chequeamos que efectivamente haya pasado por la verificación de código,
	// no confiamos en que el frontend "diga" que ya lo verificó
	var verificado bool
	err := PGPool.QueryRow(ctx,
		`SELECT verificado FROM codigos_verificacion WHERE dni = $1`, req.DNI,
	).Scan(&verificado)
	if err != nil || !verificado {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}

	socio, err := leerSocioPorDNI(ctx, req.DNI)
	if err != nil {
		http.Error(w, "Afiliado no encontrado", http.StatusBadRequest)
		return
	}

	if socio.UIDStr() != "" {
		http.Error(w, "Este DNI ya tiene una cuenta creada", http.StatusConflict)
		return
	}

	// Chequeo extra: puede que este mail YA tenga cuenta en Firebase Auth
	// por otra vía (ej. Google Sign-In previo) aunque nosotros nunca nos
	// enteramos. En ese caso no creamos una cuenta nueva, vinculamos
	// la que ya existe.
	usuarioExistente, err := AuthClient.GetUserByEmail(ctx, socio.Email)
	if err == nil && usuarioExistente != nil {
		log.Printf("DEBUG: mail %s ya tenía cuenta en Auth (uid=%s), vinculando en vez de crear", socio.Email, usuarioExistente.UID)

		// Le seteamos la password nueva a la cuenta existente
		updateParams := (&firebaseauth.UserToUpdate{}).Password(req.Password)
		if _, err := AuthClient.UpdateUser(ctx, usuarioExistente.UID, updateParams); err != nil {
			log.Printf("error actualizando password para uid existente %s: %v", usuarioExistente.UID, err)
			http.Error(w, "Error vinculando cuenta existente", 500)
			return
		}

		PGPool.Exec(ctx, `UPDATE socios SET uid = $2, actualizado_en = now() WHERE dni = $1`, req.DNI, usuarioExistente.UID)
		PGPool.Exec(ctx, `DELETE FROM codigos_verificacion WHERE dni = $1`, req.DNI)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"uid": usuarioExistente.UID, "vinculado": "true"})
		return
	}

	// Creamos el usuario real en Firebase Auth (caso normal: no existía antes)
	params := (&firebaseauth.UserToCreate{}).
		Email(socio.Email).
		Password(req.Password).
		DisplayName(socio.Nombre + " " + socio.Apellido)

	userRecord, err := AuthClient.CreateUser(ctx, params)
	if err != nil {
		log.Printf("error creando usuario en Auth para dni %s: %v", req.DNI, err)
		http.Error(w, "Error creando la cuenta", 500)
		return
	}

	// Vinculamos el UID de Auth con el socio
	PGPool.Exec(ctx, `UPDATE socios SET uid = $2, actualizado_en = now() WHERE dni = $1`, req.DNI, userRecord.UID)

	// Limpiamos el código, ya cumplió su función
	PGPool.Exec(ctx, `DELETE FROM codigos_verificacion WHERE dni = $1`, req.DNI)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"uid": userRecord.UID})
}

func CambiarPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DNI      string `json:"dni"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}

	if req.DNI == "" || req.Password == "" {
		http.Error(w, "faltan datos", http.StatusBadRequest)
		return
	}

	if !reDNI.MatchString(req.DNI) {
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}

	if len(req.Password) < 8 {
		http.Error(w, "La contraseña debe tener al menos 8 caracteres", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Mismo chequeo que CrearPassword: no confiamos en que el frontend
	// "diga" que ya verificó el código, lo confirmamos contra PG.
	var verificado bool
	err := PGPool.QueryRow(ctx,
		`SELECT verificado FROM codigos_verificacion WHERE dni = $1`, req.DNI,
	).Scan(&verificado)
	if err != nil || !verificado {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}

	socio, err := leerSocioPorDNI(ctx, req.DNI)
	if err != nil {
		http.Error(w, "Afiliado no encontrado", http.StatusBadRequest)
		return
	}

	// A diferencia de CrearPassword: acá SÍ necesitamos que ya tenga cuenta.
	// Si nunca la activó, no hay password que cambiar, tiene que hacer Primer Ingreso.
	if socio.UIDStr() == "" {
		http.Error(w, "Todavía no activaste tu cuenta. Hacé el Primer Ingreso primero.", http.StatusBadRequest)
		return
	}

	updateParams := (&firebaseauth.UserToUpdate{}).Password(req.Password)
	if _, err := AuthClient.UpdateUser(ctx, socio.UIDStr(), updateParams); err != nil {
		log.Printf("error actualizando password para uid %s (dni %s): %v", socio.UIDStr(), req.DNI, err)
		http.Error(w, "Error actualizando la contraseña", http.StatusInternalServerError)
		return
	}

	// Limpiamos el código, ya cumplió su función
	PGPool.Exec(ctx, `DELETE FROM codigos_verificacion WHERE dni = $1`, req.DNI)

	// Generamos un custom token para que el frontend pueda loguear
	// directo al usuario tras el cambio, sin pedirle que ingrese de nuevo.
	customToken, err := AuthClient.CustomToken(ctx, socio.UIDStr())
	if err != nil {
		log.Printf("error generando custom token para uid %s: %v", socio.UIDStr(), err)
		// No cortamos la respuesta por esto: la password ya se cambió bien,
		// simplemente el usuario va a tener que loguearse manualmente.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"uid": socio.UIDStr()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"uid": socio.UIDStr(), "customToken": customToken})
}
