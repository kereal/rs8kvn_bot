package subserver

import "regexp"

// subscriptionCacheKey — единственное место, которое знает раскладку ключей
// кэша: Xray живёт под голым subID (совместимость), прочие форматы — с
// суффиксом, чтобы клиент одного формата не получил тело другого.
func subscriptionCacheKey(subID string, format OutputFormat) string {
	if format == OutputXray {
		return subID
	}

	return subID + ":" + format.String()
}

// subscriptionCacheKeys перечисляет все ключи форматов одной подписки для
// инвалидации: без этого после оплаты sing-box/Clash-клиент до TTL получал
// старый лимит, пока Xray-клиент уже видел новый.
func subscriptionCacheKeys(subID string) []string {
	formats := []OutputFormat{OutputXray, OutputSingbox, OutputClash}
	keys := make([]string, 0, len(formats))

	for _, format := range formats {
		keys = append(keys, subscriptionCacheKey(subID, format))
	}

	return keys
}

func SubIDRegex() *regexp.Regexp {
	return regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
}

const MaxIPEntries = 100
const MaxDeviceEntries = 100

// SubscriptionResult represents the outcome of a subscription request.
// It carries the final body, response headers, and an optional status code
// for the HTTP layer to replay.
type SubscriptionResult struct {
	Body       []byte
	Headers    map[string]string
	StatusCode int
}
