package subserver

import "sync"

// flightGroup схлопывает одновременные промахи кэша по одному ключу: пока
// первый запрос тянет данные с нод, остальные ждут его результат вместо того,
// чтобы устроить второй fan-out на те же самые источники.
//
// Область действия намеренно узкая — только загрузка и рендер ответа. Учёт
// устройств/IP и запись last_request остаются на каждом запросе: иначе
// одновременные устройства одной подписки теряли бы друг друга.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
	// onJoin, when set, observes a follower joining an existing flight.
	onJoin func()
}

type flightCall struct {
	done chan struct{}

	result  *SubscriptionResult
	success int
	total   int
	err     error
}

// flightResult — результат одного fan-out, который раздаётся всем ожидающим.
type flightResult struct {
	result  *SubscriptionResult
	success int
	total   int
	err     error
}

// Do выполняет fn один раз для всех одновременных вызовов с тем же ключом.
func (g *flightGroup) Do(key string, fn func() flightResult) flightResult {
	g.mu.Lock()

	if g.calls == nil {
		g.calls = make(map[string]*flightCall)
	}

	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		if g.onJoin != nil {
			g.onJoin()
		}
		<-call.done

		return flightResult{result: call.result, success: call.success, total: call.total, err: call.err}
	}

	call := &flightCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		// Закрываем канал даже при панике: иначе ожидающие зависли бы навсегда.
		close(call.done)
	}()

	out := fn()
	call.result, call.success, call.total, call.err = out.result, out.success, out.total, out.err

	return out
}
