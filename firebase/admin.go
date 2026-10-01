package firebase

import (
	"context"
	"fmt"
	"log"
)

func DarAdmin(email string) error {
	ctx := context.Background()

	user, err := AuthClient.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no se encontró el usuario: %w", err)
	}

	if err := AuthClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
		"admin": true,
		"role":  "admin",
	}); err != nil {
		return fmt.Errorf("error asignando admin: %w", err)
	}

	log.Printf("✅ Admin asignado a %s | UID: %s\n", email, user.UID)

	return nil
}
