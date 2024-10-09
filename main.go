package main

import (
	"context"
	"fmt"
	"log"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
)

func main() {
	ctx := context.Background()

	p := firebaseProxy{
		projects: map[string]int{},
		port:     7777,
	}
	p.addProject("demo-admin", 9099)
	// p.addProject("demo-user", 9098)

	go func() {
		log.Fatal(p.listenAndServe())
	}()

	readyCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	err := <-p.ready(readyCtx)
	if err != nil {
		log.Panic(err)
	}

	adminApp, err := firebase.NewApp(ctx, &firebase.Config{
		ProjectID: "demo-admin",
	})
	if err != nil {
		log.Panic(err)
	}
	admin, err := adminApp.Auth(ctx)
	if err != nil {
		log.Panic(err)
	}

	_, err = admin.CreateUser(ctx,
		new(auth.UserToCreate).
			DisplayName("ADMIN").
			Email("admin@example.com").
			Password("password"),
	)
	if err != nil {
		log.Panic(err)
	}

	idToken, err := p.issueIDToken(ctx, "demo-admin", "admin@example.com", "password")
	if err != nil {
		log.Panic(err)
	}
	tok, err := admin.VerifyIDToken(ctx, idToken)
	if err != nil {
		log.Panic(err)
	}
	fmt.Printf("%#v", tok)
	fmt.Println()
}
