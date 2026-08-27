package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/NguyenHuuHoang711/asao-odoo-tools/internal/security/hmac"
	identityv1 "github.com/asao-platform/atoc/packages/proto/identity/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	signer := hmac.NewSigner("organization-service", "v1", []byte(os.Getenv("KEY")))
	conn, _ := grpc.NewClient("localhost:9081",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(signer.UnaryClientInterceptor()))
	defer conn.Close()
	c := identityv1.NewIdentityServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.ValidateToken(ctx, &identityv1.ValidateTokenRequest{Token: os.Args[1]})
	fmt.Println("err:", err)
	if resp != nil {
		fmt.Printf("valid=%v user_id=%q\n", resp.GetValid(), resp.GetUserId())
	}
}
