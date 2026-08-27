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
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firebaseauth "firebase.google.com/go/v4/auth"
)

// autenticar con Firebase Admin en el resto del backend)
var (
	FirestoreClient *firestore.Client
	AuthClient      *firebaseauth.Client
)

var RESEND_API_KEY string

// son estructuras =>

type AfiliadoPreRegistrado struct {
	Nombre   string `firestore:"nombre"`
	Apellido string `firestore:"apellido"`
	DNI      string `firestore:"dni"`
	Email    string `firestore:"email"`
	Plan     string `firestore:"plan"`
	UID      string `firestore:"uid"` // se completa recién cuando crea la cuenta
}

type CodigoVerificacion struct {
	Codigo     string    `firestore:"codigo"`
	Expira     time.Time `firestore:"expira"`
	Intentos   int       `firestore:"intentos"`
	Verificado bool      `firestore:"verificado"`
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
		Flujo string `json:"flujo"` // "primer_ingreso" o "recuperar_password"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON invalido", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Mensaje genérico siempre, exista o no el DNI — no queremos que este
	// endpoint sirva para "probar" qué DNIs están cargados en la base
	respuestaGenerica := map[string]string{"mensaje": "Si el DNI está registrado, te enviamos un código."}

	doc, err := FirestoreClient.Collection("socios").Doc(req.DNI).Get(ctx)
	if err != nil || !doc.Exists() {
		log.Printf("DEBUG: DNI %s no encontrado en afiliadosPreRegistrados (err=%v)", req.DNI, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respuestaGenerica)
		return
	}

	var afiliado AfiliadoPreRegistrado
	if err := doc.DataTo(&afiliado); err != nil {
		log.Printf("error parseando afiliado %s: %v", req.DNI, err)
		http.Error(w, "Error interno", 500)
		return
	}

	// Primer Ingreso: si ya tiene cuenta, no debería pedir código de nuevo por acá
	if req.Flujo == "primer_ingreso" && afiliado.UID != "" {
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}

	// Recuperar Password: si todavía NO tiene cuenta, no hay password que recuperar
	if req.Flujo == "recuperar_password" && afiliado.UID == "" {
		http.Error(w, "DNI inválido", http.StatusBadRequest)
		return
	}

	codigo, err := generarCodigo()
	if err != nil {
		http.Error(w, "Error generando código", 500)
		return
	}

	_, err = FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Set(ctx, CodigoVerificacion{
		Codigo:     codigo,
		Expira:     time.Now().Add(10 * time.Minute),
		Intentos:   0,
		Verificado: false,
	})
	if err != nil {
		http.Error(w, "Error guardando código", 500)
		return
	}

	if err := enviarCodigoPorMail(afiliado.Email, afiliado.Nombre, codigo); err != nil {
		log.Printf("error enviando mail a %s: %v", afiliado.Email, err)
		http.Error(w, "Error enviando el código", 500)
		return
	}

	respuestaGenerica["mailEnmascarado"] = enmascararMail(afiliado.Email)
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
	ref := FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI)
	doc, err := ref.Get(ctx)
	if err != nil || !doc.Exists() {
		http.Error(w, "Código no encontrado, solicitalo de nuevo", http.StatusBadRequest)
		return
	}

	var cv CodigoVerificacion
	if err := doc.DataTo(&cv); err != nil {
		http.Error(w, "Error interno", 500)
		return
	}

	if cv.Intentos >= 5 {
		http.Error(w, "Demasiados intentos, solicitá un código nuevo", http.StatusTooManyRequests)
		return
	}

	if time.Now().After(cv.Expira) {
		http.Error(w, "El código venció, solicitá uno nuevo", http.StatusBadRequest)
		return
	}

	if cv.Codigo != req.Codigo {
		ref.Update(ctx, []firestore.Update{{Path: "intentos", Value: cv.Intentos + 1}})
		http.Error(w, "Código incorrecto", http.StatusBadRequest)
		return
	}

	ref.Update(ctx, []firestore.Update{{Path: "verificado", Value: true}})

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
	codigoDoc, err := FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Get(ctx)
	if err != nil || !codigoDoc.Exists() {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}

	var cv CodigoVerificacion
	codigoDoc.DataTo(&cv)
	if !cv.Verificado {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}

	afiliadoRef := FirestoreClient.Collection("socios").Doc(req.DNI)
	afiliadoDoc, err := afiliadoRef.Get(ctx)
	if err != nil || !afiliadoDoc.Exists() {
		http.Error(w, "Afiliado no encontrado", http.StatusBadRequest)
		return
	}
	var afiliado AfiliadoPreRegistrado
	afiliadoDoc.DataTo(&afiliado)

	if afiliado.UID != "" {
		http.Error(w, "Este DNI ya tiene una cuenta creada", http.StatusConflict)
		return
	}

	// Chequeo extra: puede que este mail YA tenga cuenta en Firebase Auth
	// por otra vía (ej. Google Sign-In previo) aunque nuestro Firestore
	// nunca se enteró. En ese caso no creamos una cuenta nueva, vinculamos
	// la que ya existe.
	usuarioExistente, err := AuthClient.GetUserByEmail(ctx, afiliado.Email)
	if err == nil && usuarioExistente != nil {
		log.Printf("DEBUG: mail %s ya tenía cuenta en Auth (uid=%s), vinculando en vez de crear", afiliado.Email, usuarioExistente.UID)

		// Le seteamos la password nueva a la cuenta existente
		updateParams := (&firebaseauth.UserToUpdate{}).Password(req.Password)
		if _, err := AuthClient.UpdateUser(ctx, usuarioExistente.UID, updateParams); err != nil {
			log.Printf("error actualizando password para uid existente %s: %v", usuarioExistente.UID, err)
			http.Error(w, "Error vinculando cuenta existente", 500)
			return
		}

		afiliadoRef.Update(ctx, []firestore.Update{{Path: "uid", Value: usuarioExistente.UID}})
		FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Delete(ctx)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"uid": usuarioExistente.UID, "vinculado": "true"})
		return
	}

	// Creamos el usuario real en Firebase Auth (caso normal: no existía antes)
	params := (&firebaseauth.UserToCreate{}).
		Email(afiliado.Email).
		Password(req.Password).
		DisplayName(afiliado.Nombre + " " + afiliado.Apellido)

	userRecord, err := AuthClient.CreateUser(ctx, params)
	if err != nil {
		log.Printf("error creando usuario en Auth para dni %s: %v", req.DNI, err)
		http.Error(w, "Error creando la cuenta", 500)
		return
	}

	// Vinculamos el UID de Auth con el documento del afiliado
	afiliadoRef.Update(ctx, []firestore.Update{{Path: "uid", Value: userRecord.UID}})

	// Limpiamos el código, ya cumplió su función
	FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Delete(ctx)

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
	// "diga" que ya verificó el código, lo confirmamos contra Firestore.
	codigoDoc, err := FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Get(ctx)
	if err != nil || !codigoDoc.Exists() {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}
	var cv CodigoVerificacion
	codigoDoc.DataTo(&cv)
	if !cv.Verificado {
		http.Error(w, "Verificá tu código primero", http.StatusForbidden)
		return
	}

	afiliadoRef := FirestoreClient.Collection("socios").Doc(req.DNI)
	afiliadoDoc, err := afiliadoRef.Get(ctx)
	if err != nil || !afiliadoDoc.Exists() {
		http.Error(w, "Afiliado no encontrado", http.StatusBadRequest)
		return
	}
	var afiliado AfiliadoPreRegistrado
	afiliadoDoc.DataTo(&afiliado)

	// A diferencia de CrearPassword: acá SÍ necesitamos que ya tenga cuenta.
	// Si nunca la activó, no hay password que cambiar, tiene que hacer Primer Ingreso.
	if afiliado.UID == "" {
		http.Error(w, "Todavía no activaste tu cuenta. Hacé el Primer Ingreso primero.", http.StatusBadRequest)
		return
	}

	updateParams := (&firebaseauth.UserToUpdate{}).Password(req.Password)
	if _, err := AuthClient.UpdateUser(ctx, afiliado.UID, updateParams); err != nil {
		log.Printf("error actualizando password para uid %s (dni %s): %v", afiliado.UID, req.DNI, err)
		http.Error(w, "Error actualizando la contraseña", http.StatusInternalServerError)
		return
	}

	// Limpiamos el código, ya cumplió su función
	FirestoreClient.Collection("codigosVerificacion").Doc(req.DNI).Delete(ctx)

	// Generamos un custom token para que el frontend pueda loguear
	// directo al usuario tras el cambio, sin pedirle que ingrese de nuevo.
	customToken, err := AuthClient.CustomToken(ctx, afiliado.UID)
	if err != nil {
		log.Printf("error generando custom token para uid %s: %v", afiliado.UID, err)
		// No cortamos la respuesta por esto: la password ya se cambió bien,
		// simplemente el usuario va a tener que loguearse manualmente.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"uid": afiliado.UID})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"uid": afiliado.UID, "customToken": customToken})
}
