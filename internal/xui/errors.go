package xui

import "errors"

var (
	// ErrClientNotFound сигнализирует, что панель не нашла клиента по email.
	// Порождается классификацией ответов add/update/del/resetTraffic/traffic/get
	// (см. classifyPanelError); тексты сообщений панели зафиксированы в
	// doc/architecture.md, раздел «Контракт API панели 3x-ui».
	ErrClientNotFound = errors.New("client not found")

	// ErrClientAlreadyExists сигнализирует, что панель отклонила создание
	// клиента из-за занятого email или subId (add: "already in use").
	ErrClientAlreadyExists = errors.New("client already exists")

	// ErrInboundNotFound сигнализирует, что панель не нашла inbound по id
	// ("Obtain (record not found)", проверено на живой панели v3.11.0).
	// Отдельный sentinel, а не ErrClientNotFound: отсутствующий inbound — это
	// ошибка конфигурации ноды, и ветка update→create не должна на неё
	// срабатывать как на исчезнувшего клиента.
	ErrInboundNotFound = errors.New("inbound not found")

	// ErrIncompleteFanout сигнализирует, что клиент записан не на все
	// запрошенные инбаунды: одна из flow-групп не применилась или панель
	// приняла запрос, но не привязала клиента к части инбаундов. Это
	// ретраебельное состояние: повтор приводит клиента к полному членству,
	// а парковка здесь означала бы вечно полурабочую подписку.
	ErrIncompleteFanout = errors.New("client is attached to a subset of requested inbounds")
)
