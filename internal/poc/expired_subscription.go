package poc

// expiredSubscriptionFeed is deliberately independent from the upstream
// fetcher. It is a valid one-line VLESS subscription containing a harmless
// placeholder endpoint, so clients can keep the subscription URL and show a
// useful name after the real subscription expires.
const expiredSubscriptionFeed = "vless://00000000-0000-0000-0000-000000000000@expired.local:443?encryption=none&security=none&type=tcp#⏳ Подписка закончилась\n"

func expiredSubscriptionBody() []byte {
	return []byte(expiredSubscriptionFeed)
}
