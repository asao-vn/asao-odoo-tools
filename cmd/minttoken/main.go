package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/NguyenHuuHoang711/asao-odoo-tools/internal/security/hmac"
	identityv1 "github.com/asao-platform/atoc/packages/proto/identity/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Mints a real access token through identity-service's own
// BootstrapAsaoUser RPC — the repository's existing issuance path. No
// secret is known here and no token is forged.
func main() {
	subject := os.Args[1]
	// identity-service requires HMAC-signed inter-service calls. This tool
	// stands in for api-gateway, which is the component that legitimately
	// calls BootstrapAsaoUser, and signs with the local dev key from the
	// same config the service reads. It cannot forge a token: identity-service
	// still mints it.
	signer := hmac.NewSigner("api-gateway", "v1", []byte(os.Getenv("ASAO_HMAC_KEY_API_GATEWAY")))
	addr := os.Getenv("ASAO_IDENTITY_GRPC_ADDR")
	if addr == "" {
		addr = "localhost:9081"
	}
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(signer.UnaryClientInterceptor()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	c := identityv1.NewIdentityServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := c.BootstrapAsaoUser(ctx, &identityv1.BootstrapAsaoUserRequest{
		ProviderType:         "keycloak",
		ProviderIssuer:       "http://localhost:8081/realms/asao",
		ProviderSubject:      subject,
		NormalizedIdentifier: subject + "@conformance.local",
		EmailVerified:        true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "mint failed:", err)
		os.Exit(1)
	}
	out, _ := json.Marshal(map[string]string{
		"asao_user_id": resp.GetAsaoUserId(),
		"access_token": resp.GetAccessToken(),
	})
	fmt.Println(string(out))
}
