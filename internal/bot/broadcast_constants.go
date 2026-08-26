package bot

import "time"

const (
	// --- Session / UI ---

	// broadcastSessionTTL — максимальное время жизни черновика рассылки.
	broadcastSessionTTL = 15 * time.Minute
	// broadcastNameMaxLen — максимальная длина названия рассылки.
	broadcastNameMaxLen = 100
	// broadcastTextPreviewMaxRunes — максимальная длина текста в карточке рассылки.
	broadcastTextPreviewMaxRunes = 500
	// broadcastErrorTextMaxLen — максимальная длина текста ошибки в отчёте.
	broadcastErrorTextMaxLen = 500
	// broadcastErrorPreviewMaxRunes — максимальная длина текста одной ошибки
	// в карточке деталей рассылки (полный текст остаётся в delivery_report).
	broadcastErrorPreviewMaxRunes = 120

	// --- Per-message delivery ---

	// broadcastRetries — число повторов доставки одного сообщения при временных
	// (не blocked/unreachable) ошибках.
	broadcastRetries = 2
	// broadcastSendRetryBaseDelay — короткая пауза между повторами одного
	// сообщения; фактическая задержка = base * (attempt+1) (linear backoff).
	broadcastSendRetryBaseDelay = 300 * time.Millisecond

	// --- Worker / campaign ---

	// broadcastWorkerInterval — как часто worker проверяет runnable кампании.
	broadcastWorkerInterval = 15 * time.Second
	// broadcastTimeout — максимальное время обработки одной кампании.
	broadcastTimeout = 5 * time.Minute
	// broadcastConcurrency — максимальное число параллельных goroutine-отправок.
	broadcastConcurrency = 10
	// broadcastBatchSize — максимальный размер claim-батча получателей.
	broadcastBatchSize = 100

	// --- Campaign-level retry (exponential backoff) ---

	// broadcastRetryBaseDelay — начальная задержка перед повторным запуском
	// кампании после infrastructure/persistence failure.
	broadcastRetryBaseDelay = 5 * time.Second
	// broadcastRetryMaxDelay — верхняя граница exponential backoff кампании.
	broadcastRetryMaxDelay = 15 * time.Minute
)
