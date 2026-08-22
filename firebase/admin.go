package firebase

import (
	"context"
	"fmt"
)

func DarAdmin(email string) error {
	ctx := context.Background()

	user, err := AuthClient.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no se encontró el usuario: %w", err)
	}

	err = AuthClient.SetCustomUserClaims(ctx, user.UID, map[string]interface{}{
		"admin": true,
	})
	if err != nil {
		return fmt.Errorf("error asignando admin: %w", err)
	}

	fmt.Printf("✅ Admin asignado a %s | UID: %s\n", email, user.UID)

	return nil
}
