// test_firebase_ping.go
package main

import (
	"context"
	"log"

	firebase "firebase.google.com/go/v4"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	// 1) App 初期化
	app, err := firebase.NewApp(ctx, nil)
	if err != nil {
		log.Fatalf("init error: %v", err)
	}

	// 2) Auth クライアントを生成
	_, err = app.Auth(ctx)
	if err != nil {
		log.Fatalf("✗ Firebase ping failed: %v", err)
	}

	log.Println("✓ Firebase Admin SDK initialized and Auth client ready!")
}
