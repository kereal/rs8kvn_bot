# Fix: drain платёжных апдейтов при остановке + nil-контекст drain-бюджета (2026-11-25)

Контекст: ревью готовности `feature/telegram-stars` к проду. Зелёные build/vet/
`golangci-lint`/`go test ./...`, `-race` на `cmd/bot`, `internal/bot`, `internal/service`,
`internal/database`. Из найденного отобраны только два дефекта с реальным денежным
исходом; остальное осознанно принято как риск fallback-канала (Stars — запасной провайдер).

## Проблема 1: хвост `updates` выбрасывался при остановке

`runEventLoop` после `StopReceivingUpdates()` вычитывал канал в пустоту:
`go func() { for range updates {} }()`. Но `tgbotapi` (v5.5.1, `GetUpdatesChan`) выставляет
`config.Offset = update.UpdateID + 1` **до** `ch <- update`, поэтому Telegram такие апдейты
повторно не пришлёт. Рестарт/деплой под нагрузкой → `successful_payment` в буфере канала
потерян → деньги списаны, заказ висит `pending`, только ручной разбор.

## Проблема 2: nil-контекст drain-бюджета

`financialDrainBudget.contextFor` создавал контекст внутри `sync.Once`, а `stop()` вызывал
`once.Do(func(){})`. Если `stop()` успевал первым (остановка вышла за `ShutdownTimeout`,
а диспетчер ещё дорабатывал очередь), `once.Do` в `contextFor` не выполнялся, `b.ctx`
оставался nil, и `finCtx.Err()` паниковал (nil interface) — паника в горутине валит процесс.

## Фикс

`cmd/bot/lifecycle.go`:

```go
var closedDrainContext = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}()

func (b *financialDrainBudget) contextFor(parent context.Context) context.Context {
	b.once.Do(func() {
		b.ctx, b.cancel = context.WithTimeout(context.WithoutCancel(parent), shutdownFinancialTimeout)
	})

	if b.ctx == nil {
		return closedDrainContext // stop() раньше первого contextFor
	}

	return b.ctx
}

// Хвост канала разбирается общим drain-бюджетом, а не выбрасывается.
updatesWg.Go(func() {
	drainBufferedUpdates(ctx, updates, processAcceptedFinancial)
})

func drainBufferedUpdates(ctx context.Context, updates <-chan tgbotapi.Update,
	process func(context.Context, tgbotapi.Update)) {
	for update := range updates {
		process(ctx, update)
	}
}
```

`<-chan tgbotapi.Update` вместо `tgbotapi.UpdatesChannel` (последний — receive-only, в
тесте не отправить). `updatesWg.Go` вместо `Add/go/defer Done` — иначе `modernize`
(`waitgroupgo`) ругается. Горутина учтена в `updatesWg`, поэтому общий `done` её дожидается.

## Тесты и мутационная проверка (важный урок)

`cmd/bot/lifecycle_test.go`: `TestDrainBufferedUpdates_ProcessesWholeBacklog` и расширенный
`TestFinancialDrainBudget_Stop`.

Мутация «вернуть `for range updates {}`» → `TestDrainBufferedUpdates` падает (`processed == nil`).
Но мутация «снять nil-guard» ловилась не сразу: первый вариант теста вызывал
`contextFor` ДО `stop()`, где `b.ctx` уже создан, — тест проходил. Исправлено отдельным
бюджетом, остановленным до первого `contextFor`; после этого мутация падает на
`require.NotNil(t, afterStop)`.

**Урок:** для ленивой инициализации мутационный тест обязан воспроизводить порядок
`init`/`stop`, при котором ветка выполняется, а не только наличие вызова.

## Что осознанно НЕ исправляли

- `confirmStarsPayment` fail-closed при `o.stars.Load() == nil` — осознанный контракт
  (`TestConfirmStarsPayment_ClientDisabledReturnsPaymentDisabled`), без клиента мертвы и
  авто-возврат, и уведомление.
- Авто-retry потерянных `successful_payment` — Bot API не отдаёт список charge'ей бота;
  `ReconcilePendingOrders` только локально истекает Stars-pending. Уточнено в
  `doc/telegram-stars-plan.md` (убрано неверное упоминание `stars.Client.GetTransactions`).
- Аудит `ExpirePendingOrder` — шум; `/terms` вне rate limiter — требование Telegram
  Developer Terms 6.2 (намеренно); `orderByCallbackPayload` attach до валидации —
  достижимо только с валидной подписью Platega.

## Осталось до прода (операторское, не код)

Чеклист §10.1 плана: settle legacy Platega pending, XTR-товары `is_active=false` до switch,
активация XTR после switch, рабочий Stars refund client, процедура ручного разбора по
charge ID (doc/operations.md §8.1).

## Файлы

- cmd/bot/lifecycle.go, cmd/bot/lifecycle_test.go
- doc/telegram-stars-plan.md
- internal/bot/stars_callback_test.go (gofmt)

Коммиты: `148d40b` (фикс + тесты + doc + gofmt), `21bdb24` (усиленный тест после мутации).
