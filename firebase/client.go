package firebase

import (
	"context"
	"log"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

var Client *firestore.Client
var AuthClient *auth.Client

func Init() {
	ctx := context.Background()
	sa := option.WithCredentialsFile("serviceAccountKey.json")
	app, err := firebase.NewApp(ctx, nil, sa)
	if err != nil {
		log.Fatalf("error inicializando firebase: %v", err)
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("error conectando firestore: %v", err)
	}
	Client = client

	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Fatalf("error conectando auth: %v", err)
	}
	AuthClient = authClient
}
