package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/poc"
)

func main() {
	upstream := os.Getenv("UPSTREAM_SUBSCRIPTION_URL")
	if upstream == "" {
		upstream = "https://api.relayhub.surf/sub/7xyJltVORlgNrjtLHQvk6MWe"
	}
	if _, err := logger.Init("", "error"); err != nil {
		log.Fatal("failed to initialize logger")
	}
	s := poc.New(upstream)
	now := time.Now().UTC()
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		log.Fatal("failed to load Europe/Moscow timezone: ", err)
	}
	expiresAt := func(hour, minute int) time.Time {
		localNow := now.In(moscow)
		end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, moscow)
		if !end.After(now) {
			end = end.AddDate(0, 0, 1)
		}
		return end
	}
	s.Seed("A", expiresAt(14, 32).Sub(now))
	s.Seed("B", expiresAt(14, 45).Sub(now))
	s.Seed("C", expiresAt(15, 0).Sub(now))
	fmt.Println("POC pages:")
	fmt.Println(poc.PublicBaseURL + "/poc/A")
	fmt.Println(poc.PublicBaseURL + "/poc/B")
	fmt.Println(poc.PublicBaseURL + "/poc/C")
	fmt.Println("Subscription URLs:")
	fmt.Println(poc.PublicBaseURL + "/sub/A")
	fmt.Println(poc.PublicBaseURL + "/sub/B")
	fmt.Println(poc.PublicBaseURL + "/sub/C")
	log.Fatal(http.ListenAndServe("127.0.0.1:8890", s))
}
