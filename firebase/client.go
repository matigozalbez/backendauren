package firebase

import (
	"context"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

var AuthClient *auth.Client
var MessagingClient *messaging.Client

func Init() {
	ctx := context.Background()
	sa := option.WithCredentialsFile("serviceAccountKey.json")
	app, err := firebase.NewApp(ctx, nil, sa)
	if err != nil {
		log.Fatalf("error inicializando firebase: %v", err)
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Fatalf("error conectando auth: %v", err)
	}
	AuthClient = authClient

	messagingClient, err := app.Messaging(ctx)
	if err != nil {
		log.Fatalf("error conectando messaging: %v", err)
	}
	MessagingClient = messagingClient
}
