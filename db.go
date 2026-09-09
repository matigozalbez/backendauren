package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var PG *pgxpool.Pool

func InitPG() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL no configurada en .env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	PG, err = pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("No se pudo crear pool PG: %v", err)
	}

	if err := PG.Ping(ctx); err != nil {
		log.Fatalf("No se pudo conectar a PG: %v", err)
	}

	fmt.Println("PostgreSQL conectado OK")
}

func ClosePG() {
	if PG != nil {
		PG.Close()
	}
}
