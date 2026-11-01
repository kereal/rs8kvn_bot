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
)
