package bot

import "time"

const (
	// --- Session / UI ---

	// broadcastErrorTextMaxLen — максимальная длина текста ошибки в отчёте.
	broadcastErrorTextMaxLen = 500

	// --- Per-message delivery ---

	// broadcastRetries — число ПОВТОРОВ (сверх первой отправки) доставки одного
	// сообщения при временных (не blocked/unreachable) ошибках. Итого за одно
	// сообщение выполняется broadcastRetries+1 попыток отправки.
	broadcastRetries = 2
	// broadcastSendRetryBaseDelay — короткая пауза между повторами одного
	// сообщения; фактическая задержка = base * (attempt+1) (linear backoff).
	broadcastSendRetryBaseDelay = 300 * time.Millisecond
	// broadcastFloodMaxWaits — сколько раз одно сообщение может ждать по
	// retry_after (429), прежде чем исход фиксируется как постоянная ошибка.
	broadcastFloodMaxWaits = 5
	// broadcastFloodDefaultDelay — ожидание при 429 без явного retry_after.
	broadcastFloodDefaultDelay = 5 * time.Second
	// broadcastFloodMaxDelay — верхняя граница ожидания по retry_after,
	// чтобы одна подсказка не съела весь временной слайс кампании.
	broadcastFloodMaxDelay = 90 * time.Second

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
